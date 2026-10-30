package main

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Eyevinn/mp4ff/aac"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// codecString returns the RFC 6381 codecs string of an init segment.
func codecString(init *mp4.InitSegment) (string, error) {
	stsd := init.Moov.Trak.Mdia.Minf.Stbl.Stsd
	switch {
	case stsd.AvcX != nil && stsd.AvcX.AvcC != nil:
		avcC := stsd.AvcX.AvcC
		if len(avcC.SPSnalus) == 0 {
			return "", fmt.Errorf("avcC without SPS")
		}
		sps, err := avc.ParseSPSNALUnit(avcC.SPSnalus[0], false)
		if err != nil {
			return "", err
		}
		// Lower-case hex, as in the other assets of this repo.
		return strings.ToLower(avc.CodecString(stsd.AvcX.Type(), sps)), nil
	case stsd.HvcX != nil && stsd.HvcX.HvcC != nil:
		spss := stsd.HvcX.HvcC.GetNalusForType(hevc.NALU_SPS)
		if len(spss) == 0 {
			return "", fmt.Errorf("hvcC without SPS")
		}
		sps, err := hevc.ParseSPSNALUnit(spss[0])
		if err != nil {
			return "", err
		}
		return hevc.CodecString(stsd.HvcX.Type(), sps), nil
	case stsd.Av01 != nil && stsd.Av01.Av1C != nil:
		return stsd.Av01.Av1C.CodecConfRec.CodecString("av01"), nil
	case stsd.Mp4a != nil && stsd.Mp4a.Esds != nil:
		dsi := stsd.Mp4a.Esds.DecConfigDescriptor.DecSpecificInfo
		if dsi == nil {
			return "", fmt.Errorf("esds without decoder specific info")
		}
		asc, err := aac.DecodeAudioSpecificConfig(bytes.NewReader(dsi.DecConfig))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("mp4a.40.%d", asc.ObjectType), nil
	case stsd.Opus != nil:
		return "opus", nil
	case stsd.AC3 != nil:
		return "ac-3", nil
	}
	return "", fmt.Errorf("unknown sample entry")
}

// iopSimpleCodec reports whether a codecs string is one of DASH-IF IOP simple
// (IOP 4.3 8.4): AVC Main or High up to level 4.0, HEVC Main, Main tier, up
// to level 4.1, or (HE-)AAC.
func iopSimpleCodec(codecs string) bool {
	parts := strings.Split(codecs, ".")
	switch parts[0] {
	case "avc1", "avc3": // avc1.PPCCLL
		if len(parts) != 2 || len(parts[1]) != 6 {
			return false
		}
		v, err := strconv.ParseUint(parts[1], 16, 32)
		profile, level := v>>16, v&0xff
		return err == nil && (profile == 77 || profile == 100) && level <= 40
	case "hvc1", "hev1": // hvc1.P.C.TLL...
		if len(parts) < 4 || parts[1] != "1" || !strings.HasPrefix(parts[3], "L") {
			return false
		}
		level, err := strconv.Atoi(parts[3][1:])
		return err == nil && level <= 123
	}
	return codecs == "mp4a.40.2" || codecs == "mp4a.40.5" || codecs == "mp4a.40.29"
}

// requiredBandwidth returns the lowest @bandwidth (bits/s) for which the DASH
// rule holds: when the representation is delivered at that rate, starting at
// any segment, playout can start after minBufferTime and never stall. Every
// sample must have arrived by its decode time. The boxes of a segment are
// counted with its first sample.
//
// livesim2 loops the asset, so the look-ahead wraps around to the start, and
// the result is at least the average rate. The look-ahead is limited to one
// loop or 600 s, after which the average rate dominates.
func requiredBandwidth(p *packaged, minBufferTime float64) uint32 {
	ts := float64(p.timescale)
	n := len(p.samples)
	bits := make([]float64, n)
	for _, s := range p.segs {
		var payload int
		for _, m := range p.samples[s.first:s.end] {
			payload += int(m.size)
		}
		for i := s.first; i < s.end; i++ {
			bits[i] = float64(8 * p.samples[i].size)
		}
		bits[s.first] += float64(8 * (s.size - payload))
	}
	last := p.segs[len(p.segs)-1]
	loopDur := float64(last.tfdt+last.dur) / ts
	horizon := min(loopDur, 600)
	worst := averageBitrate(p)
	for _, s := range p.segs {
		t0 := float64(p.samples[s.first].dts) / ts
		sum := 0.0
		for k := 0; k < n; k++ {
			j := (s.first + k) % n
			dt := float64(p.samples[j].dts)/ts - t0
			if j < s.first {
				dt += loopDur // in the next loop
			}
			if dt >= horizon {
				break
			}
			sum += bits[j]
			worst = max(worst, sum/(minBufferTime+dt))
		}
	}
	return uint32(math.Ceil(worst))
}

// averageBitrate returns the average bitrate (bits/s) of all segment bytes.
func averageBitrate(p *packaged) float64 {
	var bytes int
	var dur uint64
	for _, s := range p.segs {
		bytes += s.size
		dur += s.dur
	}
	return 8 * float64(bytes) / (float64(dur) / float64(p.timescale))
}
