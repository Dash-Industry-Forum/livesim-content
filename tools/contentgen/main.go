// contentgen generates a DASH VoD test asset for livesim2 from a YAML config:
// video with burned-in labels (codec, bitrate, resolution, representation,
// segment and frame number, an HH:MM:SS:FF timecode, and a white flash at
// every full second), and audio with a tone at the start of every second.
//
// ffmpeg encodes each representation to a fragmented MP4. contentgen then
// cuts it into segments on the segment grid, shifts the video composition
// time offsets so that each segment is presented from its tfdt, adds edit
// lists for the audio priming where needed, computes the codecs strings and the
// @bandwidth values, and writes the MPD. See README.md.
package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed resources/Go-Mono.ttf
var monoFont []byte

//go:embed resources/dashif-logo.png
var dashifLogo []byte

// marker is written to every generated representation directory. Only such
// directories are overwritten without -force.
const marker = ".contentgen"

func main() {
	cfgPath := flag.String("config", "", "asset config file (YAML)")
	outDir := flag.String("out", "", "output directory (default: the directory of the config file)")
	duration := flag.Float64("duration", 0, "duration in seconds, overriding the config")
	suggest := flag.Float64("suggest", 0, "print the exact asset durations near this many seconds, and exit")
	ffVideo := flag.String("ffmpeg", envOr("FFMPEG", "ffmpeg"), "ffmpeg with the video encoders and drawtext ($FFMPEG)")
	ffAudio := flag.String("ffmpeg-audio", os.Getenv("FFMPEG_AUDIO"),
		"ffmpeg with the audio encoders, if not the same ($FFMPEG_AUDIO)")
	keep := flag.Bool("keep", false, "keep the intermediate files in <out>/.contentgen-tmp")
	force := flag.Bool("force", false, "overwrite representation directories that contentgen did not make")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: contentgen -config asset.yaml [options]\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *cfgPath == "" || flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	if *ffAudio == "" {
		*ffAudio = *ffVideo
	}
	opts := options{cfgPath: *cfgPath, outDir: *outDir, duration: *duration, suggest: *suggest, ffVideo: *ffVideo,
		ffAudio: *ffAudio, keep: *keep, force: *force}
	if err := run(opts); err != nil {
		log.Fatal(err)
	}
}

type options struct {
	cfgPath, outDir   string
	duration, suggest float64
	ffVideo, ffAudio  string
	keep, force       bool
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func run(o options) error {
	c, err := loadConfig(o.cfgPath)
	if err != nil {
		return err
	}
	if o.duration > 0 {
		c.Duration = o.duration
	}
	c.setDefaults()
	if o.suggest > 0 {
		if c.Video.FPS.Num == 0 || c.segFrames() == 0 {
			return fmt.Errorf("%s: -suggest needs video.fps and a segmentDuration of whole frames", o.cfgPath)
		}
		fmt.Print(c.suggest(o.suggest))
		return nil
	}
	warnings, err := c.validate()
	if err != nil {
		return fmt.Errorf("%s:\n%w", o.cfgPath, err)
	}
	for _, w := range warnings {
		fmt.Println("warning:", w)
	}

	out := o.outDir
	if out == "" {
		out = filepath.Dir(o.cfgPath)
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	if err := prepareRepDirs(c, out, o.force); err != nil {
		return err
	}
	tmp := filepath.Join(out, ".contentgen-tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	if !o.keep {
		defer os.RemoveAll(tmp)
	}
	ff := &ffmpeg{video: o.ffVideo, audio: o.ffAudio, tmpDir: tmp}
	vEnc, aEnc := encodersFor(c)
	if err := checkFeatures(ff.video, vEnc, []string{"drawtext", "rotate", "geq"}); err != nil {
		return err
	}
	if err := checkFeatures(ff.audio, aEnc, nil); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "font.ttf"), monoFont, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "logo.png"), dashifLogo, 0o644); err != nil {
		return err
	}
	logo, err := png.DecodeConfig(bytes.NewReader(dashifLogo))
	if err != nil {
		return fmt.Errorf("logo: %w", err)
	}
	files := overlayFiles{font: "font.ttf", logo: "logo.png", logoW: logo.Width, logoH: logo.Height}

	fmt.Printf("Generating %s: %g s, %d segments of %g s at %s fps, %s addressing, in %s\n",
		c.Name, c.Duration, c.nrSegments(), c.SegmentDuration, c.Video.FPS, c.Addressing, out)
	nSegs := c.nrSegments()
	var results []*repResult
	discs := make(map[int]string)
	for i := range c.Video.Representations {
		r := &c.Video.Representations[i]
		size := r.Height / 3 / 2 * 2
		disc, ok := discs[size]
		if !ok {
			disc = fmt.Sprintf("disc%d.png", size)
			if err := ff.renderDisc(disc, size); err != nil {
				return err
			}
			discs[size] = disc
		}
		fmt.Printf("  %s: %s %dx%d %d kbps ...\n", r.ID, r.Codec, r.Width, r.Height, r.Bitrate)
		enc := r.ID + ".mp4"
		files.disc = disc
		if err := ff.run(ff.video, r.ID, videoArgs(c, *r, files, enc)); err != nil {
			return err
		}
		t, err := readTrack(filepath.Join(tmp, enc))
		if err != nil {
			return err
		}
		segTicks := uint64(c.segFrames() * c.Video.FPS.frameTicks())
		p, err := planVideo(t, r.ID, segTicks, nSegs)
		if err != nil {
			return err
		}
		res, err := finish(c, t, p, out, o.keep)
		if err != nil {
			return err
		}
		res.video = r
		results = append(results, res)
	}
	for i := range c.Audio.Representations {
		r := &c.Audio.Representations[i]
		fmt.Printf("  %s: %s (%s) %d kbps, %s ...\n", r.ID, r.Codec, r.Encoder, r.Bitrate, r.Signal)
		enc := r.ID + ".mp4"
		if err := ff.run(ff.audio, r.ID, audioArgs(c, *r, enc)); err != nil {
			return err
		}
		t, err := readTrack(filepath.Join(tmp, enc))
		if err != nil {
			return err
		}
		segTicks := c.audioSegTicks()
		p, err := planAudio(t, r.ID, c.Audio.Priming, c.audioFrames(r.Codec), segTicks, nSegs)
		if err != nil {
			return err
		}
		res, err := finish(c, t, p, out, o.keep)
		if err != nil {
			return err
		}
		res.audio = r
		results = append(results, res)
	}
	mpdPath := filepath.Join(out, c.MPDName)
	if err := writeMPD(c, results, mpdPath); err != nil {
		return err
	}
	if err := saveConfig(c, o.cfgPath, out); err != nil {
		return err
	}
	report(c, results, mpdPath)
	return nil
}

// prepareRepDirs removes old representation directories made by contentgen.
// Unless force is set, it first checks that it would overwrite nothing else:
// no representation directory and no MPD that contentgen did not make.
func prepareRepDirs(c *Config, out string, force bool) error {
	var dirs []string
	for _, r := range c.Video.Representations {
		dirs = append(dirs, filepath.Join(out, r.ID))
	}
	for _, r := range c.Audio.Representations {
		dirs = append(dirs, filepath.Join(out, r.ID))
	}
	var existing []string
	for _, dir := range dirs {
		if _, err := os.Stat(dir); err == nil {
			existing = append(existing, dir)
		}
	}
	if !force {
		for _, dir := range existing {
			if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
				return fmt.Errorf("%s exists and was not made by contentgen; use -force to overwrite it", dir)
			}
		}
		mpdPath := filepath.Join(out, c.MPDName)
		if data, err := os.ReadFile(mpdPath); err == nil && !strings.Contains(string(data), mpdSource) {
			return fmt.Errorf("%s exists and was not made by contentgen; use -force to overwrite it", mpdPath)
		}
	}
	for _, dir := range existing {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}

// finish names and writes the segments of a representation, and computes
// its codecs string and bandwidth.
func finish(c *Config, t *track, p *packaged, out string, keep bool) (*repResult, error) {
	p.name(c.Addressing, *c.StartNumber)
	dir := filepath.Join(out, p.id)
	if err := p.write(t, dir); err != nil {
		return nil, err
	}
	settings, err := effectiveConfig(c)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, marker), settings, 0o644); err != nil {
		return nil, err
	}
	if !keep {
		_ = os.Remove(t.path)
	}
	codecs, err := codecString(t.init)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.id, err)
	}
	return &repResult{pkg: p, codecs: codecs, bandwidth: requiredBandwidth(p, c.MinBufferTime)}, nil
}

// effectiveConfig returns the settings that were used, with all defaults and
// overrides, as YAML.
func effectiveConfig(c *Config) ([]byte, error) {
	data, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	header := "# Settings used by livesim-content tools/contentgen to generate this asset.\n"
	return append([]byte(header), data...), nil
}

// saveConfig writes the settings that were used to <out>/contentgen.yaml,
// unless that is the config file itself. The representation directories
// have them in their marker file anyway.
func saveConfig(c *Config, cfgPath, out string) error {
	path := filepath.Join(out, "contentgen.yaml")
	if sameFile(path, cfgPath) {
		return nil
	}
	data, err := effectiveConfig(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// report prints a summary of the generated representations.
func report(c *Config, results []*repResult, mpdPath string) {
	fmt.Printf("\nWrote %s\n", mpdPath)
	fmt.Printf("%-12s %-22s %5s %8s %8s %6s %10s %s\n", "rep", "codecs", "segs", "target", "average", "", "@bandwidth", "edit list")
	for _, r := range results {
		target := 0
		if r.video != nil {
			target = r.video.Bitrate
		} else {
			target = r.audio.Bitrate
		}
		avg := averageBitrate(r.pkg)
		elst := "-"
		if r.pkg.elst > 0 {
			elst = fmt.Sprintf("%d (%.1f ms)", r.pkg.elst, 1000*float64(r.pkg.elst)/float64(r.pkg.timescale))
		}
		fmt.Printf("%-12s %-22s %5d %6dk %7.0fk %5.0f%% %10d %s\n", r.pkg.id, r.codecs, len(r.pkg.segs),
			target, avg/1000, 100*avg/float64(target*1000), r.bandwidth, elst)
		if r.pkg.notConst > 0 {
			fmt.Printf("  warning: %d segments are not presented from their first picture, so their earliest "+
				"presentation time differs from their tfdt\n", r.pkg.notConst)
		}
	}
	if c.Addressing == AddrTimelineNumber {
		fmt.Println("\nnote: livesim2 does not yet read SegmentTimeline with $Number$ (livesim2 issue #152)")
	}
}
