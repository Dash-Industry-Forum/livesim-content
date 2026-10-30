package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
)

func testConfig() *Config {
	c := &Config{
		Name: "test", Duration: 16, SegmentDuration: 2,
		Video: VideoConfig{FPS: Rate{25, 1}, Representations: []VideoRep{
			{ID: "V1", Codec: "avc", Width: 640, Height: 360, Bitrate: 300}}},
		Audio: AudioConfig{Representations: []AudioRep{{ID: "A1", Codec: "aac", Bitrate: 64}}},
	}
	c.setDefaults()
	return c
}

func TestValidate(t *testing.T) {
	cases := []struct {
		desc    string
		modify  func(c *Config)
		errPart string
		warning bool
	}{
		{"valid", func(c *Config) {}, "", false},
		{"2 s at 29.97 fps", func(c *Config) { c.Video.FPS = Rate{30000, 1001} }, "not a whole number of frames", false},
		{"3600 s of 2.002 s segments", func(c *Config) {
			c.Video.FPS, c.SegmentDuration, c.Duration = Rate{30000, 1001}, 2.002, 3600
		}, "-suggest 3600", false},
		{"64.064 s at 29.97 fps", func(c *Config) {
			c.Video.FPS, c.SegmentDuration, c.Duration = Rate{30000, 1001}, 2.002, 64.064
		}, "", false},
		{"segment not whole frames", func(c *Config) { c.SegmentDuration = 1.01 }, "whole number of frames", false},
		{"duration not whole segments", func(c *Config) { c.Duration = 15 }, "whole number of 2 s segments", false},
		{"bad addressing", func(c *Config) { c.Addressing = "list" }, "addressing", false},
		{"duplicate id", func(c *Config) { c.Audio.Representations[0].ID = "V1" }, "used twice", false},
		{"bad video codec", func(c *Config) { c.Video.Representations[0].Codec = "vp9" }, "video codec", false},
		{"odd width", func(c *Config) { c.Video.Representations[0].Width = 641 }, "even", false},
		{"bad signal", func(c *Config) { c.Audio.Representations[0].Signal = "noise" }, "signal", false},
		{"AAC not whole frames", func(c *Config) { c.Duration = 30 }, "", true},
		{"1 s GOPs", func(c *Config) { c.Video.GOP = 1 }, "", false},
		{"GOP does not divide the segment", func(c *Config) { c.Video.GOP = 0.8 }, "video.gop", false},
		{"GOP not whole frames", func(c *Config) { c.Video.GOP = 0.5 }, "video.gop", false},
		{"extra MPD", func(c *Config) {
			c.MPDs = []MPDConfig{{Name: "Manifest300.mpd", Representations: []string{"V1", "A1"}}}
		}, "", false},
		{"extra MPD with unknown representation", func(c *Config) {
			c.MPDs = []MPDConfig{{Name: "Manifest300.mpd", Representations: []string{"V2"}}}
		}, `no representation "V2"`, false},
		{"extra MPD named like the main one", func(c *Config) {
			c.MPDs = []MPDConfig{{Name: "Manifest.mpd", Representations: []string{"V1"}}}
		}, "used twice", false},
		{"extra MPD without .mpd", func(c *Config) {
			c.MPDs = []MPDConfig{{Name: "sub/Manifest300.mpd", Representations: []string{"V1"}}}
		}, "ends in .mpd", false},
	}
	for _, tc := range cases {
		c := testConfig()
		tc.modify(c)
		warnings, err := c.validate()
		switch {
		case tc.errPart == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tc.desc, err)
		case tc.errPart != "" && (err == nil || !strings.Contains(err.Error(), tc.errPart)):
			t.Errorf("%s: got error %v, want one with %q", tc.desc, err, tc.errPart)
		}
		if got := len(warnings) > 0; tc.errPart == "" && got != tc.warning {
			t.Errorf("%s: warnings %v, want warning=%t", tc.desc, warnings, tc.warning)
		}
	}
}

// testTrack makes a track with the given samples and an init with a timescale.
func testTrack(timescale uint32, samples []sampleMeta, elst int64) *track {
	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(timescale, "video", "und")
	return &track{init: init, samples: samples, elst: elst}
}

// videoSamples makes nGOP closed GOPs of gop frames. With bframes, the decode
// order is I P B B P B B ..., and ffmpeg-style negative offsets are used.
func videoSamples(nGOP, gop int, frameDur uint32, bframes bool) []sampleMeta {
	var s []sampleMeta
	for i := 0; i < nGOP*gop; i++ {
		m := sampleMeta{dts: uint64(i) * uint64(frameDur), dur: frameDur, size: 1000, sync: i%gop == 0}
		if bframes {
			// I and P one frame later, B one frame earlier, relative to decode order.
			m.cto = -int32(frameDur)
			if i%gop != 0 && i%3 != 1 {
				m.cto = -2 * int32(frameDur)
			}
		}
		s = append(s, m)
	}
	return s
}

func TestPlanVideo(t *testing.T) {
	const frameDur, gop = 3600, 50 // 25 fps, 2 s GOPs at 90 kHz
	p, err := planVideo(testTrack(90000, videoSamples(4, gop, frameDur, false), 0), "V", gop*frameDur, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.segs) != 4 || p.elst != 0 || p.segs[3].tfdt != 3*gop*frameDur {
		t.Errorf("I/P: segs=%d elst=%d", len(p.segs), p.elst)
	}

	p, err = planVideo(testTrack(90000, videoSamples(4, gop, frameDur, true), 0), "V", gop*frameDur, 4)
	if err != nil {
		t.Fatal(err)
	}
	// Offsets shifted by the offset of the first (I) frame, which then has 0.
	// No edit list, and the B-frames get negative offsets.
	if p.ctoShift != -frameDur || p.elst != 0 || p.notConst != 0 || p.samples[0].cto != 0 {
		t.Errorf("B-frames: ctoShift=%d elst=%d notConst=%d first cto=%d", p.ctoShift, p.elst, p.notConst, p.samples[0].cto)
	}
	negative := 0
	for _, s := range p.samples {
		if s.cto < 0 {
			negative++
		}
	}
	if negative == 0 {
		t.Error("B-frames: expected negative composition time offsets")
	}
	for k, s := range p.segs {
		if s.start != s.tfdt || s.mpdDur != gop*frameDur {
			t.Errorf("segment %d: start %d (tfdt %d) dur %d", k, s.start, s.tfdt, s.mpdDur)
		}
	}

	// A GOP that does not match the segment duration is an error.
	if _, err := planVideo(testTrack(90000, videoSamples(4, 40, frameDur, false), 0), "V", gop*frameDur, 4); err == nil {
		t.Error("expected an error for GOPs that do not match the segments")
	}

	// Two GOPs per segment: keyframes inside a segment are fine.
	p, err = planVideo(testTrack(90000, videoSamples(8, gop/2, frameDur, true), 0), "V", gop*frameDur, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.segs) != 4 || p.notConst != 0 || p.segs[1].first != gop {
		t.Errorf("1 s GOPs: segs=%d notConst=%d second segment starts at %d", len(p.segs), p.notConst, p.segs[1].first)
	}
}

func audioSamples(n int, frameDur uint32) []sampleMeta {
	s := make([]sampleMeta, n)
	for i := range s {
		s[i] = sampleMeta{dts: uint64(i) * uint64(frameDur), dur: frameDur, size: 300, sync: true}
	}
	return s
}

func TestPlanAudio(t *testing.T) {
	const segTicks = 96000 // 2 s at 48 kHz
	// AAC with 1024 samples of priming: strip drops one frame, elst keeps it.
	p, err := planAudio(testTrack(48000, audioSamples(400, 1024), 1024), "A", PrimingStrip, 375, segTicks, 4)
	if err != nil {
		t.Fatal(err)
	}
	if p.skip != 1 || p.elst != 0 || len(p.samples) != 375 {
		t.Errorf("strip: skip=%d elst=%d samples=%d", p.skip, p.elst, len(p.samples))
	}
	// Segments start at the first frame at or after k*2 s: 94, 94, 94, 93 frames.
	want := []int{0, 94, 188, 282}
	for k, s := range p.segs {
		if s.first != want[k] {
			t.Errorf("strip: segment %d starts at frame %d, want %d", k, s.first, want[k])
		}
	}

	p, err = planAudio(testTrack(48000, audioSamples(400, 1024), 1024), "A", PrimingElst, 375, segTicks, 4)
	if err != nil {
		t.Fatal(err)
	}
	if p.skip != 0 || p.elst != 1024 {
		t.Errorf("elst: skip=%d elst=%d", p.skip, p.elst)
	}
	// The first segment is shortened by the edit list, the others are shifted.
	if s := p.segs[0]; s.start != 0 || s.mpdDur != 94*1024-1024 {
		t.Errorf("elst: first segment start %d dur %d", s.start, s.mpdDur)
	}
	if s := p.segs[1]; s.start != 94*1024-1024 {
		t.Errorf("elst: second segment start %d", s.start)
	}

	// Opus with 312 samples of priming: less than a frame, so it all goes in the edit list.
	p, err = planAudio(testTrack(48000, audioSamples(410, 960), 312), "A", PrimingStrip, 400, segTicks, 4)
	if err != nil {
		t.Fatal(err)
	}
	if p.skip != 0 || p.elst != 312 {
		t.Errorf("opus: skip=%d elst=%d", p.skip, p.elst)
	}
}

func TestSegmentTemplateTimeline(t *testing.T) {
	c := testConfig()
	c.Addressing = AddrTimelineTime
	p, err := planAudio(testTrack(48000, audioSamples(400, 1024), 0), "A", PrimingStrip, 375, 96000, 4)
	if err != nil {
		t.Fatal(err)
	}
	p.name(c.Addressing, 1)
	st := segmentTemplate(c, p)
	if st.Media != "$RepresentationID$/$Time$.m4s" || st.StartNumber != nil {
		t.Errorf("media %q startNumber %v", st.Media, st.StartNumber)
	}
	// 94, 94, 94 frames are one S with r=2, then 93 frames.
	s := st.SegmentTimeline.S
	if len(s) != 2 || s[0].R != 2 || s[0].D != 94*1024 || s[1].D != 93*1024 || *s[0].T != 0 || s[1].T != nil {
		t.Errorf("unexpected timeline %+v %+v", s[0], s[1])
	}
	if p.segs[1].name != "96256.m4s" {
		t.Errorf("segment name %s", p.segs[1].name)
	}
}

func TestRequiredBandwidth(t *testing.T) {
	// Constant rate: 25 frames/s of 1000 bytes and no box overhead is 200 kbit/s.
	p, err := planVideo(testTrack(90000, videoSamples(4, 50, 3600, false), 0), "V", 180000, 4)
	if err != nil {
		t.Fatal(err)
	}
	for k := range p.segs {
		p.segs[k].size = 50 * 1000
	}
	if bw := requiredBandwidth(p, 2); bw != 200000 {
		t.Errorf("constant rate: got %d, want 200000", bw)
	}
	// A big first frame in every segment: 300 kbit/s on average. A 2 s
	// buffer absorbs the burst, but a 0.1 s buffer needs the frame in 0.1 s.
	for i := range p.samples {
		if p.samples[i].sync {
			p.samples[i].size = 26000
		}
	}
	for k := range p.segs {
		p.segs[k].size = 49*1000 + 26000
	}
	if bw := requiredBandwidth(p, 2); bw != 300000 {
		t.Errorf("bursty, 2 s buffer: got %d, want the average 300000", bw)
	}
	if bw := requiredBandwidth(p, 0.1); bw != 26000*8*10 {
		t.Errorf("bursty, 0.1 s buffer: got %d, want %d", bw, 26000*8*10)
	}
}

func TestVideoFilterLabels(t *testing.T) {
	// The labels come from the config, so they cannot disagree with the encoding.
	c := testConfig()
	c.Video.BFrames = 2
	files := overlayFiles{font: "font.ttf", disc: "disc.png", logo: "logo.png", logoW: 283, logoH: 100}
	f := videoFilter(c, VideoRep{ID: "V600", Codec: "hevc", Width: 960, Height: 540, Bitrate: 600, Color: "white"}, files)
	// The logo is 540/8 = 67 pixels high, and keeps its aspect ratio.
	for _, want := range []string{"HEVC 600 kbps", "960x540 25fps IPB", "Rep V600", "floor(n/50)+1", "[2:v]scale=190:67[logo]"} {
		if !strings.Contains(f, want) {
			t.Errorf("filter lacks %q", want)
		}
	}
}

func TestSubsetMPD(t *testing.T) {
	c := testConfig()
	c.Video.Representations = append(c.Video.Representations,
		VideoRep{ID: "V2", Codec: "avc", Width: 960, Height: 540, Bitrate: 600})
	c.MPDs = []MPDConfig{{Name: "Manifest300.mpd", Representations: []string{"A1", "V1"}}}
	c.setDefaults()
	var results []*repResult
	for i := range c.Video.Representations {
		r := &c.Video.Representations[i]
		p, err := planVideo(testTrack(90000, videoSamples(4, 50, 3600, false), 0), r.ID, 180000, 4)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, &repResult{pkg: p, codecs: "avc1.64001e", video: r})
	}
	p, err := planAudio(testTrack(48000, audioSamples(400, 1024), 0), "A1", PrimingStrip, 375, 96000, 4)
	if err != nil {
		t.Fatal(err)
	}
	results = append(results, &repResult{pkg: p, codecs: "mp4a.40.2", audio: &c.Audio.Representations[0]})

	path := t.TempDir() + "/Manifest300.mpd"
	md := c.MPDs[0]
	if err := writeMPD(c, md.Title, selectReps(results, md.Representations), path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	mpd := string(data)
	// Only the listed representations, in config order, and maxWidth and
	// maxHeight from them, not from the whole asset.
	profiles := `profiles="urn:mpeg:dash:profile:isoff-live:2011,urn:mpeg:dash:profile:cmaf:2019,` +
		`http://dashif.org/guidelines/dash-if-simple"`
	for _, want := range []string{`id="V1"`, `id="A1"`, `maxWidth="640"`, `maxHeight="360"`, c.Title, profiles} {
		if !strings.Contains(mpd, want) {
			t.Errorf("subset MPD lacks %s:\n%s", want, mpd)
		}
	}
	if strings.Contains(mpd, `id="V2"`) || strings.Index(mpd, `id="V1"`) > strings.Index(mpd, `id="A1"`) {
		t.Errorf("unexpected representations or order:\n%s", mpd)
	}
}

func TestMPDProfiles(t *testing.T) {
	const live, cmaf = "urn:mpeg:dash:profile:isoff-live:2011", "urn:mpeg:dash:profile:cmaf:2019"
	for _, tc := range []struct {
		codecs []string
		want   string
	}{
		{[]string{"avc1.64001f", "mp4a.40.2"}, live + "," + cmaf + ",http://dashif.org/guidelines/dash-if-simple"},
		{[]string{"hvc1.1.6.L123.90", "mp4a.40.29"}, live + "," + cmaf + ",http://dashif.org/guidelines/dash-if-simple"},
		{[]string{"avc1.640028", "avc1.64002a"}, live + "," + cmaf}, // 1080p60 is level 4.2
		{[]string{"hvc1.2.4.L120.90"}, live + "," + cmaf},           // Main 10
		{[]string{"hvc1.1.6.H120.90"}, live + "," + cmaf},           // High tier
		{[]string{"avc1.42c01e"}, live + "," + cmaf},                // Baseline
		{[]string{"av01.0.08M.08", "opus"}, live + "," + cmaf},
		{[]string{"avc1.64001f", "ac-3"}, live + "," + cmaf},
	} {
		var reps []*repResult
		for _, codecs := range tc.codecs {
			reps = append(reps, &repResult{codecs: codecs})
		}
		if got := string(mpdProfiles(reps)); got != tc.want {
			t.Errorf("mpdProfiles(%v) = %s, want %s", tc.codecs, got, tc.want)
		}
	}
}

func TestWriteInitBrands(t *testing.T) {
	// ffmpeg's video init has no cmfc brand; writeInit adds it.
	init := mp4.NewMP4Init()
	init.AddChild(mp4.NewFtyp("iso5", 512, []string{"iso5", "iso6", "mp41"}))
	moov := mp4.NewMoovBox()
	init.AddChild(moov)
	moov.AddChild(mp4.CreateMvhd())
	moov.AddChild(mp4.NewMvexBox())
	init.AddEmptyTrack(90000, "video", "und")
	path := t.TempDir() + "/init.mp4"
	if err := writeInit(init, 0, path); err != nil {
		t.Fatal(err)
	}
	f, err := mp4.ReadMP4File(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Init.Ftyp.CompatibleBrands(); strings.Join(got, ",") != "iso5,iso6,mp41,cmfc" {
		t.Errorf("compatible brands %v, want iso5,iso6,mp41,cmfc", got)
	}
}

func TestRatio(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		want string
	}{{1920, 1080, "16:9"}, {640, 480, "4:3"}, {720, 576, "5:4"}} {
		if got := ratio(tc.w, tc.h); got != tc.want {
			t.Errorf("ratio(%d, %d) = %s, want %s", tc.w, tc.h, got, tc.want)
		}
	}
}

func TestParseRate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Rate
		ok   bool
	}{
		{"25", Rate{25, 1}, true}, {"29.97", Rate{30000, 1001}, true}, {"60000/1001", Rate{60000, 1001}, true},
		{"23.976", Rate{24000, 1001}, true}, {"50/2", Rate{25, 1}, true}, {"0", Rate{}, false}, {"29.5", Rate{}, false},
	} {
		got, err := parseRate(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("parseRate(%q) = %v, %v", tc.in, got, err)
		}
	}
	if ts, ft := (Rate{60000, 1001}).timescale(), (Rate{60000, 1001}).frameTicks(); ts != 60000 || ft != 1001 {
		t.Errorf("59.94 fps: timescale %d, frame %d", ts, ft)
	}
}

func TestLoopSegments(t *testing.T) {
	c := testConfig()
	if k := c.loopSegments(); k != 4 {
		t.Errorf("25 fps, 2 s, AAC: %d segments, want 4 (8 s)", k)
	}
	c.Video.FPS, c.SegmentDuration = Rate{30000, 1001}, 2.002
	if k := c.loopSegments(); k != 32 {
		t.Errorf("29.97 fps, 2.002 s, AAC: %d segments, want 32", k)
	}
	if s := c.suggest(3600); !strings.Contains(s, "shortest: 64.064 s") || !strings.Contains(s, "3587.584 s (59:47.584, 1792 segments)") {
		t.Errorf("unexpected suggestion:\n%s", s)
	}
	c.Audio.Representations = append(c.Audio.Representations,
		AudioRep{ID: "A2", Codec: "opus", Bitrate: 64}, AudioRep{ID: "A3", Codec: "ac3", Bitrate: 192})
	if k := c.loopSegments(); k != 160 {
		t.Errorf("29.97 fps, 2.002 s, AAC, Opus and AC-3: %d segments, want 160", k)
	}
}

func TestTimecodeText(t *testing.T) {
	if tc := timecodeText(Rate{25, 1}); strings.Contains(tc, ";") || !strings.Contains(tc, "mod(n\\,25)") {
		t.Errorf("25 fps timecode: %s", tc)
	}
	// Drop-frame at 29.97: 17982 frames per ten minutes, 1798 per minute, 2 numbers dropped.
	tc := timecodeText(Rate{30000, 1001})
	for _, want := range []string{";", "floor(n/17982)", "/1798)", "18*floor"} {
		if !strings.Contains(tc, want) {
			t.Errorf("29.97 fps timecode lacks %q: %s", want, tc)
		}
	}
}

func TestAudioAdaptationSets(t *testing.T) {
	c := testConfig()
	c.Audio.Representations = []AudioRep{
		{ID: "A64", Codec: "aac", Bitrate: 64}, {ID: "A128", Codec: "aac", Bitrate: 128},
		{ID: "Aother", Codec: "aac", Bitrate: 96, Label: "other"}}
	c.setDefaults()
	var results []*repResult
	for i := range c.Audio.Representations {
		p, err := planAudio(testTrack(48000, audioSamples(400, 1024), 0), c.Audio.Representations[i].ID,
			PrimingStrip, 375, 96000, 4)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, &repResult{pkg: p, codecs: "mp4a.40.2", audio: &c.Audio.Representations[i]})
	}
	path := t.TempDir() + "/Manifest.mpd"
	if err := writeMPD(c, c.Title, results, path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	mpd := string(data)
	// The same codec, signal, language and label make one Adaptation Set; another label makes another.
	if n := strings.Count(mpd, "<AdaptationSet"); n != 2 {
		t.Errorf("got %d Adaptation Sets, want 2:\n%s", n, mpd)
	}
	if !strings.Contains(mpd, `<Label>beep aac</Label>`) || !strings.Contains(mpd, `<Label>other</Label>`) {
		t.Errorf("unexpected labels:\n%s", mpd)
	}

	// Representations with different segment timing cannot share an Adaptation Set.
	p, _ := planAudio(testTrack(48000, audioSamples(400, 1024), 1024), "A128", PrimingElst, 375, 96000, 4)
	results[1].pkg = p
	if err := writeMPD(c, c.Title, results, path); err == nil || !strings.Contains(err.Error(), "different segment timing") {
		t.Errorf("expected a segment timing error, got %v", err)
	}
}
