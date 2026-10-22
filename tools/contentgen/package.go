package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Eyevinn/mp4ff/mp4"
)

// sampleMeta is the timing and size of one sample, without its data.
type sampleMeta struct {
	dts  uint64
	dur  uint32
	size uint32
	cto  int32
	sync bool
}

// track is a track encoded by ffmpeg, as a fragmented MP4 file.
type track struct {
	path    string
	init    *mp4.InitSegment
	samples []sampleMeta
	elst    int64 // media_time of ffmpeg's edit list, 0 if none
}

// segment is one output segment.
type segment struct {
	first, end int    // samples [first, end) of the packaged samples
	tfdt       uint64 // decode time of the first sample
	dur        uint64 // sum of the sample durations
	ept        uint64 // earliest presentation time, before the edit list
	start      uint64 // start in the MPD timeline, after the edit list
	mpdDur     uint64 // duration in the MPD timeline
	nr         int    // segment number
	name       string // file name
	size       int    // file size in bytes
}

// packaged is a planned (and later written) representation.
type packaged struct {
	id        string
	timescale uint32
	skip      int          // input samples to skip (audio priming)
	samples   []sampleMeta // packaged samples, re-anchored to decode time 0
	ctoShift  int32        // subtracted from all composition time offsets
	elst      int64        // edit list media_time, 0 for none
	segs      []segment
	notConst  int // segments whose EPT-tfdt differs from the first one (video)
}

// forEachFragment calls fn for the init segment and then for every fragment
// of a fragmented MP4 file, reading one top-level box at a time.
func forEachFragment(path string, onInit func(*mp4.InitSegment) error, onFrag func(*mp4.Fragment) error) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	r := bufio.NewReaderSize(fh, 1<<20)
	init := mp4.NewMP4Init()
	var moof *mp4.MoofBox
	var pos uint64
	for {
		box, err := mp4.DecodeBox(pos, r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%s at byte %d: %w", path, pos, err)
		}
		pos += box.Size()
		switch b := box.(type) {
		case *mp4.FtypBox:
			init.AddChild(b)
		case *mp4.MoovBox:
			init.AddChild(b)
			if onInit != nil {
				if err := onInit(init); err != nil {
					return err
				}
			}
		case *mp4.MoofBox:
			moof = b
		case *mp4.MdatBox:
			if moof == nil {
				return fmt.Errorf("%s: mdat without moof at byte %d", path, pos)
			}
			frag := mp4.NewFragment()
			frag.AddChild(moof)
			frag.AddChild(b)
			if err := onFrag(frag); err != nil {
				return err
			}
			moof = nil
		}
	}
	return nil
}

// readTrack reads the init segment and the timing of all samples (pass 1).
func readTrack(path string) (*track, error) {
	t := &track{path: path}
	onInit := func(init *mp4.InitSegment) error {
		if len(init.Moov.Traks) != 1 || init.Moov.Mvex == nil {
			return fmt.Errorf("%s: expected one fragmented track", path)
		}
		t.init = init
		if edts := init.Moov.Trak.Edts; edts != nil && len(edts.Elst) > 0 && len(edts.Elst[0].Entries) > 0 {
			t.elst = max(edts.Elst[0].Entries[0].MediaTime, 0)
		}
		return nil
	}
	onFrag := func(frag *mp4.Fragment) error {
		if t.init == nil {
			return fmt.Errorf("%s: fragment before moov", path)
		}
		fss, err := frag.GetFullSamples(t.init.Moov.Mvex.Trex)
		if err != nil {
			return err
		}
		for _, s := range fss {
			t.samples = append(t.samples, sampleMeta{dts: s.DecodeTime, dur: s.Dur, size: s.Size,
				cto: s.CompositionTimeOffset, sync: s.IsSync()})
		}
		return nil
	}
	if err := forEachFragment(path, onInit, onFrag); err != nil {
		return nil, err
	}
	if len(t.samples) == 0 {
		return nil, fmt.Errorf("%s: no samples", path)
	}
	return t, nil
}

// planVideo cuts video into segments at the keyframes on the segment grid.
// Composition time offsets are made non-negative, and the edit list removes
// the offset of the first picture, so that the presentation starts at 0.
func planVideo(t *track, id string, segTicks uint64, nSegs int) (*packaged, error) {
	p := &packaged{id: id, timescale: t.init.Moov.Trak.Mdia.Mdhd.Timescale}
	d0 := t.samples[0].dts
	minCTO := t.samples[0].cto
	for _, s := range t.samples {
		minCTO = min(minCTO, s.cto)
	}
	p.ctoShift = minCTO
	p.samples = make([]sampleMeta, len(t.samples))
	for i, s := range t.samples {
		s.dts -= d0
		s.cto -= minCTO
		p.samples[i] = s
	}
	if !p.samples[0].sync {
		return nil, fmt.Errorf("%s: first sample is not a keyframe", id)
	}
	starts := []int{0}
	for i, s := range p.samples {
		next := uint64(len(starts)) * segTicks
		if s.dts < next {
			continue
		}
		if !s.sync || s.dts != next {
			return nil, fmt.Errorf("%s: no keyframe at %d (sample %d at %d, sync=%t); the GOP does not match the segment duration",
				id, next, i, s.dts, s.sync)
		}
		starts = append(starts, i)
	}
	if len(starts) != nSegs {
		return nil, fmt.Errorf("%s: got %d segments, expected %d", id, len(starts), nSegs)
	}
	p.segs = makeSegments(p.samples, starts)
	last := p.segs[len(p.segs)-1]
	if total := last.tfdt + last.dur; total != uint64(nSegs)*segTicks {
		return nil, fmt.Errorf("%s: video duration %d, expected %d", id, total, uint64(nSegs)*segTicks)
	}
	p.elst = int64(p.segs[0].ept - p.segs[0].tfdt)
	for _, s := range p.segs {
		if int64(s.ept-s.tfdt) != p.elst {
			p.notConst++
		}
	}
	p.setTimeline()
	return p, nil
}

// planAudio removes the encoder priming (mode strip: whole frames, the rest
// in an edit list; mode elst: all of it in an edit list), keeps the number
// of frames that covers the duration, and starts every segment with the
// first frame at or after the corresponding video segment start.
func planAudio(t *track, id, mode string, frames int, segTicks uint64, nSegs int) (*packaged, error) {
	p := &packaged{id: id, timescale: t.init.Moov.Trak.Mdia.Mdhd.Timescale}
	frameDur := uint64(t.samples[0].dur)
	switch mode {
	case PrimingStrip:
		p.skip = int(uint64(t.elst) / frameDur)
		p.elst = t.elst % int64(frameDur)
	default:
		p.elst = t.elst
	}
	if p.skip+frames > len(t.samples) {
		return nil, fmt.Errorf("%s: %d frames, need %d after skipping %d", id, len(t.samples), frames, p.skip)
	}
	kept := t.samples[p.skip : p.skip+frames]
	d0 := kept[0].dts
	p.samples = make([]sampleMeta, len(kept))
	for i, s := range kept {
		if uint64(s.dur) != frameDur {
			return nil, fmt.Errorf("%s: frame %d has duration %d, not %d", id, i, s.dur, frameDur)
		}
		s.dts -= d0
		p.samples[i] = s
	}
	starts := []int{0}
	for i, s := range p.samples {
		if len(starts) < nSegs && s.dts >= uint64(len(starts))*segTicks {
			starts = append(starts, i)
		}
	}
	if len(starts) != nSegs {
		return nil, fmt.Errorf("%s: got %d segments, expected %d", id, len(starts), nSegs)
	}
	p.segs = makeSegments(p.samples, starts)
	p.setTimeline()
	return p, nil
}

// makeSegments makes segments that start at the given sample indices.
func makeSegments(samples []sampleMeta, starts []int) []segment {
	segs := make([]segment, len(starts))
	for k, first := range starts {
		end := len(samples)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		s := segment{first: first, end: end, tfdt: samples[first].dts}
		s.ept = samples[first].dts + uint64(samples[first].cto)
		for _, m := range samples[first:end] {
			s.dur += uint64(m.dur)
			s.ept = min(s.ept, m.dts+uint64(m.cto))
		}
		segs[k] = s
	}
	return segs
}

// setTimeline sets the start and duration of every segment in the MPD
// timeline, which is the presentation time after the edit list. A first
// segment that starts before 0 is shortened.
func (p *packaged) setTimeline() {
	elst := uint64(p.elst)
	for k := range p.segs {
		s := &p.segs[k]
		end := s.ept + s.dur
		if k+1 < len(p.segs) {
			end = p.segs[k+1].ept
		}
		start := max(s.ept, elst) - elst
		s.start = start
		s.mpdDur = end - elst - start
	}
}

// name sets the segment numbers and file names.
func (p *packaged) name(addressing string, startNumber int) {
	for k := range p.segs {
		s := &p.segs[k]
		s.nr = startNumber + k
		if addressing == AddrTimelineTime {
			s.name = fmt.Sprintf("%d.m4s", s.start)
		} else {
			s.name = fmt.Sprintf("%d.m4s", s.nr)
		}
	}
}

// write writes init.mp4 and the segments of a planned representation to dir
// (pass 2), streaming the samples from the encoded file.
func (p *packaged) write(t *track, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeInit(t.init, p.elst, filepath.Join(dir, "init.mp4")); err != nil {
		return err
	}
	trex := t.init.Moov.Mvex.Trex
	trackID := t.init.Moov.Trak.Tkhd.TrackID
	in := 0 // input sample index
	k := 0  // current segment
	var seg *mp4.MediaSegment
	var frag *mp4.Fragment
	flush := func() error {
		var buf bytes.Buffer
		if err := seg.Encode(&buf); err != nil {
			return err
		}
		p.segs[k].size = buf.Len()
		return os.WriteFile(filepath.Join(dir, p.segs[k].name), buf.Bytes(), 0o644)
	}
	done := errors.New("done")
	err := forEachFragment(t.path, nil, func(f *mp4.Fragment) error {
		fss, err := f.GetFullSamples(trex)
		if err != nil {
			return err
		}
		for _, fs := range fss {
			j := in - p.skip // packaged sample index
			in++
			if j < 0 {
				continue
			}
			if j >= len(p.samples) {
				return done
			}
			if j == p.segs[k].end {
				if err := flush(); err != nil {
					return err
				}
				k++
				seg = nil
			}
			if seg == nil {
				seg = mp4.NewMediaSegment()
				seg.EncOptimize = mp4.OptimizeTrun
				if frag, err = mp4.CreateFragment(uint32(k+1), trackID); err != nil {
					return err
				}
				seg.AddFragment(frag)
			}
			fs.DecodeTime = p.samples[j].dts
			fs.CompositionTimeOffset -= p.ctoShift
			frag.AddFullSample(fs)
		}
		return nil
	})
	if err != nil && !errors.Is(err, done) {
		return err
	}
	if k != len(p.segs)-1 || seg == nil {
		return fmt.Errorf("%s: wrote %d of %d segments", p.id, k, len(p.segs))
	}
	return flush()
}

// writeInit writes the init segment with the given edit list (none for 0).
func writeInit(init *mp4.InitSegment, elst int64, path string) error {
	trak := init.Moov.Trak
	children := make([]mp4.Box, 0, len(trak.Children)+1)
	var edts *mp4.EdtsBox
	if elst > 0 {
		e := &mp4.ElstBox{Entries: []mp4.ElstEntry{{SegmentDuration: 0, MediaTime: elst, MediaRateInteger: 1}}}
		edts = &mp4.EdtsBox{Elst: []*mp4.ElstBox{e}, Children: []mp4.Box{e}}
	}
	for _, c := range trak.Children {
		if _, ok := c.(*mp4.EdtsBox); ok {
			continue
		}
		children = append(children, c)
		if _, ok := c.(*mp4.TkhdBox); ok && edts != nil {
			children = append(children, edts) // edts follows tkhd
		}
	}
	trak.Children = children
	trak.Edts = edts
	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
