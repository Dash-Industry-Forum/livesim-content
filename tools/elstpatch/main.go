// elstpatch checks the start of the presentation timeline of livesim2
// representations and, with -write, fixes it by adding a single-entry CMAF
// edit list (elst) to init.mp4. Media segments are never changed.
//
//   - video: media_time = earliest presentation time of the first segment, so
//     the first picture is presented at time 0 (e.g. 6000 @ 90kHz when the
//     first frame has a 2-frame composition offset at 30fps).
//   - audio: media_time = encoder priming given by -priming (e.g. 1024 for one
//     AAC-LC frame), so the priming is not presented. The priming cannot be
//     seen in the segments, and may already have been cut away, so it must be
//     measured (e.g. by decoding) and given explicitly. Audio is left alone
//     without -priming.
//
// The entry follows CMAF and livesim2: segment_duration=0 and media_rate=1,
// i.e. a constant shift of all presentation times. An existing no-op entry
// (media_time=0) is replaced; any other existing edit list is left alone.
//
// Each argument is a representation directory with init.mp4 and *.m4s
// segments. For every representation it reports track type, timescale,
// existing elst, segment count, total duration, decode-time continuity and
// the per-segment offset EPT-tfdt (earliest presentation time minus
// baseMediaDecodeTime). For video that offset should be the same in every
// segment, since the edit list shifts them all by the same amount.
//
// Writing is refused if mp4ff does not re-encode the original init.mp4 byte
// for byte, so that nothing but the added edts box can change.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Eyevinn/mp4ff/mp4"
)

func main() {
	write := flag.Bool("write", false, "write the edit list to init.mp4 (default: only report)")
	video := flag.Bool("video", true, "shift video so the first picture is presented at time 0")
	priming := flag.Int64("priming", 0, "audio priming in audio timescale ticks (0: leave audio alone)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: elstpatch [-write] [-video=false] [-priming N] repdir...\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	seen := make(map[string]string)
	failed := false
	for _, dir := range flag.Args() {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			log.Printf("%s: %v", dir, err)
			failed = true
			continue
		}
		if prev, ok := seen[real]; ok {
			fmt.Printf("%s: same directory as %s, skipped\n\n", dir, prev)
			continue
		}
		seen[real] = dir
		if err := run(dir, *write, *video, *priming); err != nil {
			log.Printf("%s: %v", dir, err)
			failed = true
		}
		fmt.Println()
	}
	if failed {
		os.Exit(1)
	}
}

// segInfo holds the timing of one media segment.
type segInfo struct {
	name    string
	tfdt    uint64 // baseMediaDecodeTime of the first fragment
	ept     int64  // earliest presentation time
	end     uint64 // decode time after the last sample
	sync    bool   // first sample is a sync sample
	hasSidx bool   // segment has a sidx box
}

func run(dir string, write, video bool, priming int64) error {
	initPath := filepath.Join(dir, "init.mp4")
	raw, err := os.ReadFile(initPath)
	if err != nil {
		return err
	}
	f, err := mp4.DecodeFile(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decode init: %w", err)
	}
	if f.Init == nil || f.Init.Moov == nil || len(f.Init.Moov.Traks) != 1 {
		return fmt.Errorf("expected an init segment with exactly one track")
	}
	initSeg := f.Init
	trak := initSeg.Moov.Trak
	hdlr := trak.Mdia.Hdlr.HandlerType
	timescale := trak.Mdia.Mdhd.Timescale
	codec := "?"
	if stsd := trak.Mdia.Minf.Stbl.Stsd; stsd != nil && len(stsd.Children) > 0 {
		codec = stsd.Children[0].Type()
	}

	segs, err := readSegments(dir, initSeg.Moov.Mvex.Trex)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return fmt.Errorf("no .m4s segments")
	}

	var gaps, nonSync, withSidx int
	offsets := make(map[int64]int)
	for i, s := range segs {
		offsets[s.ept-int64(s.tfdt)]++
		if i > 0 && s.tfdt != segs[i-1].end {
			gaps++
		}
		if !s.sync {
			nonSync++
		}
		if s.hasSidx {
			withSidx++
		}
	}
	first, last := segs[0], segs[len(segs)-1]
	dur := last.end - first.tfdt
	fmt.Printf("%s: %s %s timescale=%d segs=%d (%s..%s) tfdt0=%d dur=%d (%.3fs)\n",
		dir, hdlr, codec, timescale, len(segs), first.name, last.name, first.tfdt, dur,
		float64(dur)/float64(timescale))
	fmt.Printf("  decode-time gaps=%d  non-sync segment starts=%d  segments with sidx=%d\n",
		gaps, nonSync, withSidx)
	fmt.Printf("  EPT-tfdt: %s\n", formatOffsets(offsets, timescale))

	cur, err := currentElst(trak)
	if err != nil {
		return err
	}
	if cur == nil {
		fmt.Printf("  elst: none\n")
	} else {
		fmt.Printf("  elst: media_time=%d segment_duration=%d rate=%d.%d\n",
			cur.MediaTime, cur.SegmentDuration, cur.MediaRateInteger, cur.MediaRateFraction)
	}

	var mediaTime int64
	switch {
	case hdlr == "vide" && video:
		mediaTime = first.ept
		if len(offsets) > 1 {
			fmt.Printf("  note: EPT-tfdt varies; elst uses the first segment's EPT\n")
		}
	case hdlr == "soun" && priming > 0:
		if codec != "mp4a" {
			return fmt.Errorf("audio codec %q: -priming is meant for AAC (mp4a)", codec)
		}
		mediaTime = first.ept + priming
	case hdlr == "soun":
		fmt.Printf("  -> no change (audio needs -priming)\n")
		return nil
	default:
		fmt.Printf("  -> no change for %s track\n", hdlr)
		return nil
	}
	if mediaTime <= 0 {
		fmt.Printf("  -> no change, presentation already starts at 0\n")
		return nil
	}
	ms := 1000 * float64(mediaTime) / float64(timescale)
	noOp := false
	if cur != nil {
		isShift := cur.SegmentDuration == 0 && cur.MediaRateInteger == 1 && cur.MediaRateFraction == 0
		switch {
		case isShift && cur.MediaTime == mediaTime:
			fmt.Printf("  -> already has elst media_time=%d (%.3fms)\n", mediaTime, ms)
			return nil
		case isShift && cur.MediaTime == 0:
			noOp = true
		default:
			return fmt.Errorf("has a different elst; not touching it")
		}
	}
	if !write {
		fmt.Printf("  -> would set elst media_time=%d (%.3fms)\n", mediaTime, ms)
		return nil
	}

	// Encode the whole box tree, since InitSegment.Encode drops top-level boxes
	// other than ftyp and moov (e.g. skip). Only write if mp4ff reproduces the
	// original file exactly.
	f.FragEncMode = mp4.EncModeBoxTree
	var rt bytes.Buffer
	if err := f.Encode(&rt); err != nil {
		return fmt.Errorf("re-encode init: %w", err)
	}
	if !bytes.Equal(rt.Bytes(), raw) {
		return fmt.Errorf("mp4ff round trip of init.mp4 is not byte-exact; refusing to write")
	}
	if noOp {
		cur.MediaTime = mediaTime
		if mediaTime > 0xffffffff {
			trak.Edts.Elst[0].Version = 1
		}
	} else {
		addElst(trak, mediaTime)
	}
	var out bytes.Buffer
	if err := f.Encode(&out); err != nil {
		return fmt.Errorf("encode init: %w", err)
	}
	tmp := initPath + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, initPath); err != nil {
		return err
	}
	fmt.Printf("  -> wrote elst media_time=%d (%.3fms), init.mp4 %d -> %d bytes\n",
		mediaTime, ms, len(raw), out.Len())
	return nil
}

// readSegments decodes all *.m4s files in dir and returns them in decode order.
func readSegments(dir string, trex *mp4.TrexBox) ([]segInfo, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.m4s"))
	if err != nil {
		return nil, err
	}
	segs := make([]segInfo, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		sf, err := mp4.DecodeFile(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		si := segInfo{name: filepath.Base(name)}
		nSamples := 0
		for _, seg := range sf.Segments {
			if seg.Sidx != nil {
				si.hasSidx = true
			}
			for _, frag := range seg.Fragments {
				fss, err := frag.GetFullSamples(trex)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", name, err)
				}
				for _, s := range fss {
					pt := s.PresentationTime()
					if nSamples == 0 {
						si.tfdt = s.DecodeTime
						si.ept = pt
						si.sync = s.IsSync()
					}
					if pt < si.ept {
						si.ept = pt
					}
					si.end = s.DecodeTime + uint64(s.Dur)
					nSamples++
				}
			}
		}
		if nSamples == 0 {
			return nil, fmt.Errorf("%s: no samples", name)
		}
		segs = append(segs, si)
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].tfdt < segs[j].tfdt })
	return segs, nil
}

func currentElst(trak *mp4.TrakBox) (*mp4.ElstEntry, error) {
	if trak.Edts == nil {
		return nil, nil
	}
	if len(trak.Edts.Elst) != 1 || len(trak.Edts.Elst[0].Entries) != 1 {
		return nil, fmt.Errorf("edts is not a single elst with a single entry")
	}
	return &trak.Edts.Elst[0].Entries[0], nil
}

// addElst inserts edts/elst right after tkhd, where ISO/IEC 14496-12 puts it.
func addElst(trak *mp4.TrakBox, mediaTime int64) {
	elst := &mp4.ElstBox{
		Entries: []mp4.ElstEntry{{SegmentDuration: 0, MediaTime: mediaTime, MediaRateInteger: 1}},
	}
	if mediaTime > 0xffffffff {
		elst.Version = 1
	}
	edts := &mp4.EdtsBox{Elst: []*mp4.ElstBox{elst}, Children: []mp4.Box{elst}}
	children := make([]mp4.Box, 0, len(trak.Children)+1)
	for _, c := range trak.Children {
		children = append(children, c)
		if _, ok := c.(*mp4.TkhdBox); ok {
			children = append(children, edts)
		}
	}
	trak.Children = children
	trak.Edts = edts
}

func formatOffsets(offsets map[int64]int, timescale uint32) string {
	keys := make([]int64, 0, len(offsets))
	for k := range offsets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d (%.3fms) x%d", k, 1000*float64(k)/float64(timescale), offsets[k]))
	}
	return strings.Join(parts, ", ")
}
