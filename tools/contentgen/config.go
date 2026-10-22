package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Addressing modes of the generated MPD.
const (
	AddrNumber         = "number"          // SegmentTemplate with @duration and $Number$
	AddrTimelineTime   = "timeline-time"   // SegmentTimeline with $Time$
	AddrTimelineNumber = "timeline-number" // SegmentTimeline with $Number$
)

// Priming modes for audio.
const (
	PrimingStrip = "strip" // drop whole priming frames, signal any rest with an edit list
	PrimingElst  = "elst"  // keep all priming and signal it with an edit list
)

const audioRate = 48000

// Config describes one asset. It is kept as a YAML file next to the generated
// asset, so that the asset can be regenerated.
type Config struct {
	Name            string      `yaml:"name"`
	Title           string      `yaml:"title"`
	Duration        float64     `yaml:"duration"`        // seconds, a whole number of segments
	SegmentDuration float64     `yaml:"segmentDuration"` // seconds
	Addressing      string      `yaml:"addressing"`
	StartNumber     *int        `yaml:"startNumber"`
	MinBufferTime   float64     `yaml:"minBufferTime"` // seconds
	MPDName         string      `yaml:"mpdName"`
	Video           VideoConfig `yaml:"video"`
	Audio           AudioConfig `yaml:"audio"`
}

// VideoConfig is common to all video representations.
type VideoConfig struct {
	FPS             Rate       `yaml:"fps"`     // 25, 30000/1001, 29.97, ...
	BFrames         int        `yaml:"bframes"` // 0 gives I and P frames only
	Noise           int        `yaml:"noise"`   // temporal noise strength 0-50, 0 = off
	Representations []VideoRep `yaml:"representations"`
}

// VideoRep is one video representation.
type VideoRep struct {
	ID      string `yaml:"id"`
	Codec   string `yaml:"codec"` // avc, hevc or av1
	Width   int    `yaml:"width"`
	Height  int    `yaml:"height"`
	Bitrate int    `yaml:"bitrate"` // kbps
	Color   string `yaml:"color"`   // text color of the overlays
}

// AudioConfig is common to all audio representations.
type AudioConfig struct {
	Priming         string     `yaml:"priming"`
	Representations []AudioRep `yaml:"representations"`
}

// AudioRep is one audio representation. Representations with the same codec,
// signal, language and label share an Adaptation Set, as bitrate alternatives.
type AudioRep struct {
	ID      string   `yaml:"id"`
	Codec   string   `yaml:"codec"`   // aac, opus or ac3
	Encoder string   `yaml:"encoder"` // ffmpeg encoder, default from codec
	Bitrate int      `yaml:"bitrate"` // kbps
	Signal  string   `yaml:"signal"`  // beep or scale
	Noise   *float64 `yaml:"noise"`   // background noise level in dBFS, 0 = off, default -50
	Lang    string   `yaml:"lang"`
	Label   string   `yaml:"label"`
}

var (
	idRE    = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	langRE  = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]+)*$`)
	colors  = []string{"white", "yellow", "orange", "cyan", "lime", "pink"}
	vCodecs = map[string]string{"avc": "AVC", "hevc": "HEVC", "av1": "AV1"}
	// Default ffmpeg encoder and samples per frame (at 48kHz) per audio codec.
	aEncoders   = map[string]string{"aac": "aac", "opus": "libopus", "ac3": "ac3"}
	aFrameSizes = map[string]int{"aac": 1024, "opus": 960, "ac3": 1536}
)

// loadConfig reads a YAML config. Unknown fields are errors, to catch typos.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// setDefaults fills in optional values.
func (c *Config) setDefaults() {
	if c.Addressing == "" {
		c.Addressing = AddrNumber
	}
	if c.StartNumber == nil {
		one := 1
		c.StartNumber = &one
	}
	if c.MinBufferTime == 0 {
		c.MinBufferTime = c.SegmentDuration
	}
	if c.MPDName == "" {
		c.MPDName = "Manifest.mpd"
	}
	if c.Title == "" {
		c.Title = fmt.Sprintf("%s: generated test content with %gs segments", c.Name, c.SegmentDuration)
	}
	for i := range c.Video.Representations {
		r := &c.Video.Representations[i]
		if r.Color == "" {
			r.Color = colors[i%len(colors)]
		}
	}
	if c.Audio.Priming == "" {
		c.Audio.Priming = PrimingStrip
	}
	for i := range c.Audio.Representations {
		r := &c.Audio.Representations[i]
		if r.Encoder == "" {
			r.Encoder = aEncoders[r.Codec]
		}
		if r.Noise == nil {
			// Just enough noise that the encoders use the bitrate; pure tones need far less.
			r.Noise = new(float64)
			*r.Noise = -50
		}
		if r.Signal == "" {
			r.Signal = "beep"
		}
		if r.Lang == "" {
			r.Lang = "und"
		}
		if r.Label == "" {
			r.Label = fmt.Sprintf("%s %s", r.Signal, r.Codec)
		}
	}
}

// segFrames returns the number of video frames per segment.
func (c *Config) segFrames() int {
	n, _ := c.Video.FPS.frames(c.SegmentDuration)
	return n
}

// totalFrames returns the number of video frames of the asset.
func (c *Config) totalFrames() int {
	n, _ := c.Video.FPS.frames(c.Duration)
	return n
}

// nrSegments returns the number of segments of the asset.
func (c *Config) nrSegments() int {
	return c.totalFrames() / c.segFrames()
}

// audioSegTicks returns the segment duration in audio samples. It is only
// exact if the config is valid.
func (c *Config) audioSegTicks() uint64 {
	return uint64(c.segFrames() * c.Video.FPS.Den * audioRate / c.Video.FPS.Num)
}

// validate checks the config. It returns warnings for things that work, but
// not perfectly, such as audio that cannot loop seamlessly.
func (c *Config) validate() (warnings []string, err error) {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if !idRE.MatchString(c.Name) {
		add("name %q: use letters, digits, '_', '.' and '-'", c.Name)
	}
	if c.Duration <= 0 {
		add("duration must be > 0 seconds")
	}
	fps := c.Video.FPS
	if fps.Num <= 0 || fps.Den <= 0 {
		add("video.fps is missing")
	} else if c.SegmentDuration <= 0 {
		add("segmentDuration must be > 0")
	} else if sf, ok := fps.frames(c.SegmentDuration); !ok || sf == 0 {
		add("segmentDuration %g s is not a whole number of frames at %s fps", c.SegmentDuration, fps)
	} else {
		if len(c.Audio.Representations) > 0 && (sf*fps.Den*audioRate)%fps.Num != 0 {
			add("segmentDuration %g s is not a whole number of samples at %d Hz", c.SegmentDuration, audioRate)
		}
		if tf, ok := fps.frames(c.Duration); c.Duration > 0 && (!ok || tf%sf != 0) {
			add("duration %g s is not a whole number of %g s segments at %s fps; "+
				"-suggest %g lists durations that are", c.Duration, c.SegmentDuration, fps, c.Duration)
		}
	}
	switch c.Addressing {
	case AddrNumber, AddrTimelineTime, AddrTimelineNumber:
	default:
		add("addressing %q: use %s, %s or %s", c.Addressing, AddrNumber, AddrTimelineTime, AddrTimelineNumber)
	}
	if *c.StartNumber < 0 {
		add("startNumber must be >= 0")
	}
	if c.MinBufferTime <= 0 {
		add("minBufferTime must be > 0")
	}
	if c.Video.BFrames < 0 || c.Video.BFrames > 8 {
		add("video.bframes %d: use 0-8", c.Video.BFrames)
	}
	if c.Video.Noise < 0 || c.Video.Noise > 50 {
		add("video.noise %d: use 0-50", c.Video.Noise)
	}
	if len(c.Video.Representations) == 0 {
		add("at least one video representation is needed (it is the timing reference)")
	}
	if c.Audio.Priming != PrimingStrip && c.Audio.Priming != PrimingElst {
		add("audio.priming %q: use %s or %s", c.Audio.Priming, PrimingStrip, PrimingElst)
	}

	ids := make(map[string]bool)
	checkID := func(id string) {
		if !idRE.MatchString(id) {
			add("representation id %q: use letters, digits, '_', '.' and '-'", id)
		}
		if ids[id] {
			add("representation id %q is used twice", id)
		}
		ids[id] = true
	}
	for _, r := range c.Video.Representations {
		checkID(r.ID)
		if _, ok := vCodecs[r.Codec]; !ok {
			add("%s: video codec %q: use avc, hevc or av1", r.ID, r.Codec)
		}
		if r.Width <= 0 || r.Height <= 0 || r.Width%2 != 0 || r.Height%2 != 0 {
			add("%s: width and height must be positive and even", r.ID)
		}
		if r.Bitrate <= 0 {
			add("%s: bitrate must be > 0 kbps", r.ID)
		}
	}
	for _, r := range c.Audio.Representations {
		checkID(r.ID)
		fs, ok := aFrameSizes[r.Codec]
		if !ok {
			add("%s: audio codec %q: use aac, opus or ac3", r.ID, r.Codec)
			continue
		}
		if r.Bitrate <= 0 {
			add("%s: bitrate must be > 0 kbps", r.ID)
		}
		if r.Signal != "beep" && r.Signal != "scale" {
			add("%s: signal %q: use beep or scale", r.ID, r.Signal)
		}
		if *r.Noise > 0 {
			add("%s: noise is a level in dBFS and must be negative (or 0 for none)", r.ID)
		}
		if !langRE.MatchString(r.Lang) {
			add("%s: lang %q is not a language code", r.ID, r.Lang)
		}
		if samples := c.totalFrames() * fps.Den * audioRate; fps.Num > 0 && samples%(fps.Num*fs) != 0 {
			warnings = append(warnings, fmt.Sprintf(
				"%s: %g s is not a whole number of %s frames (%d samples), so the audio is up to one frame "+
					"longer than the video and does not loop seamlessly; -suggest %g lists durations that are",
				r.ID, c.Duration, r.Codec, fs, c.Duration))
		}
	}
	return warnings, errors.Join(errs...)
}

// audioFrames returns the number of audio frames that cover the duration.
func (c *Config) audioFrames(codec string) int {
	fps := c.Video.FPS
	samples := c.totalFrames() * fps.Den * audioRate // divided by fps.Num
	frame := fps.Num * aFrameSizes[codec]
	return (samples + frame - 1) / frame
}
