package main

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ffmpeg runs ffmpeg binaries and writes their output to log files.
type ffmpeg struct {
	video  string // ffmpeg for video (needs the video encoders and drawtext)
	audio  string // ffmpeg for audio
	tmpDir string
}

// run runs ffmpeg with args, logging to tmpDir/<name>.log.
func (f *ffmpeg) run(bin, name string, args []string) error {
	logPath := filepath.Join(f.tmpDir, name+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "%s %s\n\n", bin, strings.Join(args, " "))
	cmd := exec.Command(bin, args...)
	cmd.Dir = f.tmpDir // inputs and outputs are given relative to it
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed (%w), see %s:\n%s", name, err, logPath, tail(logPath, 10))
	}
	return nil
}

// tail returns the last n lines of a file.
func tail(path string, n int) string {
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// checkFeatures checks that bin has the named encoders and filters.
func checkFeatures(bin string, encoders, filters []string) error {
	list := func(kind string) (string, error) {
		out, err := exec.Command(bin, "-hide_banner", "-"+kind).Output()
		if err != nil {
			return "", fmt.Errorf("cannot run %s: %w (set -ffmpeg or FFMPEG)", bin, err)
		}
		return string(out), nil
	}
	has := func(out, name string) bool {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] == name {
				return true
			}
		}
		return false
	}
	var missing []string
	for kind, names := range map[string][]string{"encoders": encoders, "filters": filters} {
		if len(names) == 0 {
			continue
		}
		out, err := list(kind)
		if err != nil {
			return err
		}
		for _, n := range names {
			if !has(out, n) {
				missing = append(missing, n)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s lacks %s", bin, strings.Join(missing, ", "))
	}
	return nil
}

// discSupersampling is how many times larger the disc is drawn before it is
// scaled down, for anti-aliasing.
const discSupersampling = 4

// videoEncoders maps codec to ffmpeg encoder.
var videoEncoders = map[string]string{"avc": "libx264", "hevc": "libx265", "av1": "libsvtav1"}

// renderDisc draws the rotating disc (eight colored sectors in a black rim) to a PNG.
//
// The disc is drawn at discSupersampling times the size and scaled down with
// area averaging, so that every pixel gets the coverage of the shapes and the
// rim and sector edges are anti-aliased.
func (f *ffmpeg) renderDisc(path string, size int) error {
	big := size * discSupersampling
	c := float64(big) / 2
	// Distances from the center of the disc to the center of the pixel.
	x, y := fmt.Sprintf("(X+0.5-%g)", c), fmt.Sprintf("(Y+0.5-%g)", c)
	// Sector 0..7 by angle, colored like color bars.
	sec := fmt.Sprintf(`floor((atan2(%s\,%s)+PI)*4/PI)`, y, x)
	in := fmt.Sprintf(`lt(hypot(%s\,%s)\,%g)`, x, y, 0.9*c)
	rim := fmt.Sprintf(`lt(hypot(%s\,%s)\,%g)`, x, y, c)
	comp := func(on ...int) string {
		terms := make([]string, len(on))
		for i, s := range on {
			terms[i] = fmt.Sprintf(`eq(%s\,%d)`, sec, s)
		}
		return fmt.Sprintf(`'255*%s*(%s)'`, in, strings.Join(terms, "+"))
	}
	// Outside the disc, the color is black like the rim, so that averaging the
	// unpremultiplied colors and alpha gives no fringe at the outer edge.
	graph := fmt.Sprintf(`color=c=black@0:s=%dx%d,format=rgba,geq=r=%s:g=%s:b=%s:a='255*%s',scale=%d:%d:flags=area`,
		big, big, comp(0, 1, 4, 5), comp(0, 1, 2, 3), comp(0, 2, 4, 6), rim, size, size)
	return f.run(f.video, "disc", []string{"-hide_banner", "-y", "-f", "lavfi", "-i", graph,
		"-frames:v", "1", "-update", "1", path})
}

// overlayFiles are the files that the video filter graph uses, given
// relative to the ffmpeg working directory.
type overlayFiles struct {
	font, disc, logo string
	logoW, logoH     int // size of the logo image in pixels
}

// videoFilter builds the filter graph: testsrc2 (input 0) with optional noise,
// the rotating disc (input 1), the DASH-IF logo (input 2), and overlays
// generated from the config.
func videoFilter(c *Config, r VideoRep, files overlayFiles) string {
	fps, h, w := c.Video.FPS, r.Height, r.Width
	fs := max(h/22, 10) // label font size
	lh := fs * 14 / 10  // label line height
	mg := max(h/30, 4)  // margin
	bb := max(fs/6, 2)  // text box border
	big := h / 7        // timecode font size
	fpsF := float64(fps.Num) / float64(fps.Den)
	period := int(math.Round(10 * fpsF))      // one turn of the disc in 10 s
	sf := c.segFrames()                       // frames per segment
	flash := max(int(math.Round(fpsF/10)), 1) // flash for the first 100 ms of every second
	flashSize := max(h/9, 8)                  // flash square
	// The logo is never scaled up, since the only official version is small.
	logoH := min(h/8, files.logoH)
	logoW := (logoH*files.logoW/files.logoH + 1) / 2 * 2
	logoPad := max(logoH/10, 2)
	structure := "IP"
	if c.Video.BFrames > 0 {
		structure = "IPB"
		if r.Codec == "av1" {
			structure = "RA" // random access with hidden frames, no reordering in the container
		}
	}
	text := func(s, x, y string, size int) string {
		return fmt.Sprintf(`drawtext=fontfile=%s:text='%s':fontcolor=%s:fontsize=%d:box=1:boxcolor=black@0.6:boxborderw=%d:x=%s:y=%s`,
			files.font, s, r.Color, size, bb, x, y)
	}
	line := func(i int, s string) string {
		return text(s, fmt.Sprint(mg), fmt.Sprint(mg+i*lh), fs)
	}
	var b strings.Builder
	b.WriteString("[0:v]")
	if c.Video.Noise > 0 {
		fmt.Fprintf(&b, "noise=alls=%d:allf=t:all_seed=4711,", c.Video.Noise)
	}
	b.WriteString("null[bg];")
	// The angle is wrapped with mod(): with an unbounded angle, rotate stops
	// resolving distinct angles after about 2000 rad and the disc freezes.
	fmt.Fprintf(&b, `[1:v]rotate='2*PI*mod(n\,%d)/%d':c=none:ow=rotw(iw):oh=roth(ih)[disc];`, period, period)
	fmt.Fprintf(&b, "[bg][disc]overlay=x=%d:y=main_h-overlay_h-%d:shortest=1,", mg, mg)
	// testsrc2 draws a small clock of fixed size at the top left. Hide it,
	// and put a dark panel behind the labels.
	b.WriteString("drawbox=x=0:y=0:w=104:h=40:color=black:t=fill,")
	fmt.Fprintf(&b, "drawbox=x=0:y=0:w=%d:h=%d:color=black@0.6:t=fill,", w*42/100, mg+5*lh+bb)
	b.WriteString(line(0, fmt.Sprintf(`%s %d kbps`, vCodecs[r.Codec], r.Bitrate)) + ",")
	b.WriteString(line(1, fmt.Sprintf(`%dx%d %s %s`, w, h, fps.label(), structure)) + ",")
	b.WriteString(line(2, fmt.Sprintf(`Rep %s`, r.ID)) + ",")
	b.WriteString(line(3, fmt.Sprintf(`Seg %%{eif\:floor(n/%d)+%d\:d}`, sf, *c.StartNumber)) + ",")
	b.WriteString(line(4, `Frame %{frame_num}`) + ",")
	b.WriteString(text(timecodeText(fps), "(w-text_w)/2", "(h-text_h)/2", big) + ",")
	// A black square that is white for the first frames of every second,
	// from the first frame at or after the second, when the audio beep
	// starts. Black otherwise, so that it can be measured.
	fmt.Fprintf(&b, `drawbox=x=iw-%d-%d:y=%d:w=%d:h=%d:color=black:t=fill,`, flashSize, mg, mg, flashSize, flashSize)
	fmt.Fprintf(&b, `drawbox=x=iw-%d-%d:y=%d:w=%d:h=%d:color=white:t=fill:enable='lt(n-ceil(floor(n*%d/%d)*%d/%d)\,%d)',`,
		flashSize, mg, mg, flashSize, flashSize, fps.Den, fps.Num, fps.Num, fps.Den, flash)
	// The DASH-IF logo at the bottom right, on a white plate so that its
	// black text is readable on any background.
	fmt.Fprintf(&b, "drawbox=x=iw-%d-%d:y=ih-%d-%d:w=%d:h=%d:color=white:t=fill[main];",
		mg, logoW+2*logoPad, mg, logoH+2*logoPad, logoW+2*logoPad, logoH+2*logoPad)
	fmt.Fprintf(&b, "[2:v]scale=%d:%d[logo];", logoW, logoH)
	fmt.Fprintf(&b, "[main][logo]overlay=x=W-%d-%d:y=H-%d-%d[v]", mg+logoPad, logoW, mg+logoPad, logoH)
	return b.String()
}

// timecodeText returns the drawtext text of an HH:MM:SS:FF timecode of the
// frame number n. At 29.97 and 59.94 fps, it is a SMPTE drop-frame timecode
// (HH:MM:SS;FF), which skips frame numbers to follow the clock. Other
// non-integer rates count frames in their rounded-up base.
func timecodeText(r Rate) string {
	base := r.nominal()
	n, sep := "n", `\:`
	if d := r.dropFrames(); d > 0 {
		per10 := base*600 - 9*d // frames in ten minutes
		perMin := base*60 - d   // frames in a minute that drops numbers
		m := fmt.Sprintf(`mod(n\,%d)`, per10)
		n = fmt.Sprintf(`(n+%d*floor(n/%d)+if(lt(%s\,%d)\,0\,%d*floor((%s-%d)/%d)))`, 9*d, per10, m, d, d, m, d, perMin)
		sep = ";"
	}
	field := func(expr string) string { return fmt.Sprintf(`%%{eif\:%s\:d\:2}`, expr) }
	return field(fmt.Sprintf(`floor(%s/%d)`, n, base*3600)) + `\:` +
		field(fmt.Sprintf(`mod(floor(%s/%d)\,60)`, n, base*60)) + `\:` +
		field(fmt.Sprintf(`mod(floor(%s/%d)\,60)`, n, base)) + sep +
		field(fmt.Sprintf(`mod(%s\,%d)`, n, base))
}

// videoArgs returns the ffmpeg arguments that encode one video representation
// to a fragmented MP4 with one fragment per GOP.
func videoArgs(c *Config, r VideoRep, files overlayFiles, out string) []string {
	fps := c.Video.FPS
	gop := c.segFrames()
	frames := c.totalFrames()
	br := r.Bitrate
	args := []string{"-hide_banner", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=%s", r.Width, r.Height, fps),
		"-loop", "1", "-framerate", fps.String(), "-i", files.disc,
		"-loop", "1", "-framerate", fps.String(), "-i", files.logo,
		"-filter_complex", videoFilter(c, r, files), "-map", "[v]",
		"-frames:v", fmt.Sprint(frames), "-pix_fmt", "yuv420p", "-an"}
	switch r.Codec {
	case "avc":
		args = append(args, "-c:v", "libx264", "-preset", "medium", "-profile:v", "high",
			"-x264-params", fmt.Sprintf("keyint=%d:min-keyint=%d:scenecut=0:open-gop=0:bframes=%d:force-cfr=1",
				gop, gop, c.Video.BFrames),
			"-b:v", fmt.Sprintf("%dk", br), "-maxrate", fmt.Sprintf("%dk", br), "-bufsize", fmt.Sprintf("%dk", 2*br))
	case "hevc":
		args = append(args, "-c:v", "libx265", "-preset", "medium", "-tag:v", "hvc1",
			"-x265-params", fmt.Sprintf("keyint=%d:min-keyint=%d:scenecut=0:open-gop=0:bframes=%d:"+
				"vbv-maxrate=%d:vbv-bufsize=%d:info=0:log-level=warning", gop, gop, c.Video.BFrames, br, 2*br),
			"-b:v", fmt.Sprintf("%dk", br))
	case "av1":
		// Low delay (pred-struct=1) gives no reordering, but SVT-AV1 then needs CBR (rc=2).
		// Otherwise random access with VBR. irefresh-type=2 makes every keyframe a KEY frame.
		params := fmt.Sprintf("keyint=%d:scd=0:irefresh-type=2", gop)
		if c.Video.BFrames == 0 {
			params += ":pred-struct=1:rc=2"
		} else {
			params += ":rc=1"
		}
		args = append(args, "-c:v", "libsvtav1", "-preset", "8", "-svtav1-params", params,
			"-b:v", fmt.Sprintf("%dk", br))
	}
	return append(args, "-video_track_timescale", fmt.Sprint(fps.timescale()),
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", out)
}

// signalExpr returns the aevalsrc expression of the test signal: a 0.5 s tone
// (5 ms attack, 200 ms release) every second. It starts with the first video
// frame at or after the second, which is exactly at the second for integer
// frame rates, so that it coincides with the white square of the video.
//   - beep: 880 Hz, but 1760 Hz at every full minute.
//   - scale: a C major scale, one note per second, repeating every 8 s.
func signalExpr(signal string, noiseDB float64, fps Rate) string {
	var freq string
	switch signal {
	case "scale":
		// Scale step d = 0..7 gives the semitones 0 2 4 5 7 9 11 12.
		freq = `261.6256*pow(2\,(2*mod(floor(t)\,8)-gte(mod(floor(t)\,8)\,3)-gte(mod(floor(t)\,8)\,7))/12)`
	default:
		freq = `880*(1+eq(mod(floor(t)\,60)\,0))`
	}
	u := `mod(t\,1)` // time since the tone started
	if fps.Den != 1 {
		u = fmt.Sprintf(`(t-ceil(floor(t)*%d/%d)*%d/%d)`, fps.Num, fps.Den, fps.Den, fps.Num)
	}
	env := strings.ReplaceAll(`gte(U\,0)*lt(U\,0.5)*min(1\,U/0.005)*min(1\,(0.5-U)/0.2)`, "U", u)
	expr := fmt.Sprintf(`0.5*%s*sin(2*PI*%s*t)`, env, freq)
	if noiseDB < 0 {
		expr += fmt.Sprintf(`+%.6f*(2*random(0)-1)`, math.Pow(10, noiseDB/20))
	}
	return expr
}

// audioArgs returns the ffmpeg arguments that encode one audio representation
// to a fragmented MP4 with one frame per fragment. The source is longer than
// the asset, to cover the encoder priming that is removed later.
func audioArgs(c *Config, r AudioRep, out string) []string {
	expr := signalExpr(r.Signal, *r.Noise, c.Video.FPS)
	src := fmt.Sprintf(`aevalsrc=exprs='%s|%s':s=%d:d=%g`, expr, expr, audioRate, c.Duration+1)
	frames := c.audioFrames(r.Codec) + 8
	args := []string{"-hide_banner", "-y", "-f", "lavfi", "-i", src,
		"-frames:a", fmt.Sprint(frames), "-c:a", r.Encoder, "-b:a", fmt.Sprintf("%dk", r.Bitrate)}
	if r.Encoder == "libopus" {
		args = append(args, "-vbr", "off") // constant bitrate, so the bitrate is used
	}
	return append(args, "-ar", fmt.Sprint(audioRate), "-ac", "2",
		"-metadata:s:a:0", "language="+r.Lang,
		"-movflags", "+cmaf+separate_moof+delay_moov+frag_every_frame", "-f", "mp4", out)
}

// sameFile tells whether two paths are the same file (to avoid checking twice).
func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// encodersFor lists the ffmpeg encoders the config needs.
func encodersFor(c *Config) (video, audio []string) {
	seen := make(map[string]bool)
	for _, r := range c.Video.Representations {
		if e := videoEncoders[r.Codec]; !seen[e] {
			seen[e] = true
			video = append(video, e)
		}
	}
	for _, r := range c.Audio.Representations {
		if !seen[r.Encoder] {
			seen[r.Encoder] = true
			audio = append(audio, r.Encoder)
		}
	}
	return video, audio
}
