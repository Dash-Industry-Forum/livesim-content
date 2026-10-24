// audioseg post-processes an encoded fragmented AAC mp4 into clean, loopable
// livesim2 segments:
//
//  1. drops the AAC encoder priming frames (read from the elst mediaTime) so
//     the real audio shifts up to start at decode time 0,
//  2. keeps exactly -frames content frames (so total audio is a known length;
//     for a DURATION that is a multiple of 8s this is exact at 48kHz, since
//     8s = 375 AAC frames),
//  3. removes the elst (edit list) from the moov,
//  4. re-segments onto the 2s grid so every audio segment starts at the first
//     frame at or after the corresponding video segment boundary (ceil rule),
//     writing <out>/init.mp4 + <out>/1.m4s .. N.m4s with tfdt anchored at 0.
//
// Input is expected to be a fragmented file with one sample per fragment
// (ffmpeg -movflags frag_every_frame). Uniform sample duration is assumed
// (AAC: 1024 ticks at 48000).
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/Eyevinn/mp4ff/mp4"
)

func main() {
	inPath := flag.String("in", "", "input fragmented AAC mp4 (frag_every_frame, with priming)")
	outDir := flag.String("out", "", "output representation dir for init.mp4 + N.m4s")
	nFrames := flag.Int("frames", 0, "exact number of content frames to keep after priming")
	segSamples := flag.Uint64("segsamples", 96000, "segment duration in media timescale ticks (2s @ 48000)")
	flag.Parse()
	if *inPath == "" || *outDir == "" || *nFrames <= 0 {
		flag.Usage()
		log.Fatal("need -in, -out and -frames")
	}
	if err := run(*inPath, *outDir, *nFrames, *segSamples); err != nil {
		log.Fatalf("%s: %v", *inPath, err)
	}
}

func run(inPath, outDir string, nFrames int, segSamples uint64) error {
	r, err := os.Open(inPath)
	if err != nil {
		return err
	}
	f, err := mp4.DecodeFile(r)
	_ = r.Close()
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if f.Init == nil || !f.IsFragmented() {
		return fmt.Errorf("expected a fragmented file with an init segment")
	}
	trak := f.Init.Moov.Trak
	trex := f.Init.Moov.Mvex.Trex
	trackID := trak.Tkhd.TrackID

	// Gather all samples in decode order.
	var all []mp4.FullSample
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			ss, err := frag.GetFullSamples(trex)
			if err != nil {
				return fmt.Errorf("get samples: %w", err)
			}
			all = append(all, ss...)
		}
	}
	if len(all) == 0 {
		return fmt.Errorf("no samples in input")
	}
	sampleDur := all[0].Dur // 1024 for AAC@48000

	// Priming = whole frames to drop, from the elst mediaTime.
	priming := 0
	if trak.Edts != nil && len(trak.Edts.Elst) > 0 && len(trak.Edts.Elst[0].Entries) > 0 {
		mt := trak.Edts.Elst[0].Entries[0].MediaTime
		if mt > 0 && sampleDur > 0 && uint64(mt)%uint64(sampleDur) == 0 {
			priming = int(uint64(mt) / uint64(sampleDur))
		}
	}
	if priming+nFrames > len(all) {
		return fmt.Errorf("not enough frames: have %d, need %d priming + %d content", len(all), priming, nFrames)
	}
	kept := all[priming : priming+nFrames]

	// Re-anchor decode times so the first kept frame starts at 0.
	for i := range kept {
		kept[i].DecodeTime = uint64(i) * uint64(sampleDur)
	}

	// Remove the edit list (both the typed field and the Children slice).
	if trak.Edts != nil {
		newCh := trak.Children[:0]
		for _, c := range trak.Children {
			if _, ok := c.(*mp4.EdtsBox); ok {
				continue
			}
			newCh = append(newCh, c)
		}
		trak.Children = newCh
		trak.Edts = nil
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := mp4.WriteToFile(f.Init, filepath.Join(outDir, "init.mp4")); err != nil {
		return fmt.Errorf("write init: %w", err)
	}

	// Re-segment onto the 2s grid: a new segment starts at the first frame whose
	// decode time is >= segSamples*segNr (ceil rule => 94,94,94,93,... pattern,
	// exact 8s boundaries). tfdt is set automatically from the first sample.
	segNr := 1
	var seg *mp4.MediaSegment
	var frag *mp4.Fragment
	writeSeg := func() error {
		return mp4.WriteToFile(seg, filepath.Join(outDir, fmt.Sprintf("%d.m4s", segNr)))
	}
	startSeg := func() error {
		seg = mp4.NewMediaSegment()
		seg.EncOptimize = mp4.OptimizeTrun // move constant sample dur/flags to tfhd defaults
		fr, err := mp4.CreateFragment(uint32(segNr), trackID)
		if err != nil {
			return err
		}
		frag = fr
		seg.AddFragment(fr)
		return nil
	}
	if err := startSeg(); err != nil {
		return err
	}
	for _, s := range kept {
		if s.DecodeTime >= segSamples*uint64(segNr) {
			if err := writeSeg(); err != nil {
				return err
			}
			segNr++
			if err := startSeg(); err != nil {
				return err
			}
		}
		frag.AddFullSample(s)
	}
	if err := writeSeg(); err != nil {
		return err
	}

	fmt.Printf("%s: init + %d segments, %d content frames (dropped %d priming), total=%d ticks (%.6fs)\n",
		outDir, segNr, nFrames, priming, uint64(nFrames)*uint64(sampleDur),
		float64(uint64(nFrames)*uint64(sampleDur))/float64(trak.Mdia.Mdhd.Timescale))
	return nil
}
