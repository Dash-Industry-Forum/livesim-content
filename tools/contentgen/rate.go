package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rate is a video frame rate of Num/Den frames per second.
type Rate struct{ Num, Den int }

// namedRates are the usual names of the 1001-based rates.
var namedRates = map[string]Rate{
	"23.976": {24000, 1001}, "23.98": {24000, 1001}, "29.97": {30000, 1001}, "59.94": {60000, 1001},
}

// parseRate parses an integer rate like 25, a rational like 30000/1001, or
// one of the names 23.976, 29.97 and 59.94.
func parseRate(s string) (Rate, error) {
	s = strings.TrimSpace(s)
	if r, ok := namedRates[s]; ok {
		return r, nil
	}
	num, den, found := strings.Cut(s, "/")
	n, err := strconv.Atoi(num)
	d := 1
	if err == nil && found {
		d, err = strconv.Atoi(den)
	}
	if err != nil || n <= 0 || d <= 0 {
		return Rate{}, fmt.Errorf("fps %q: use an integer like 25, a ratio like 30000/1001, or 23.976, 29.97 or 59.94", s)
	}
	g := gcd(n, d)
	return Rate{n / g, d / g}, nil
}

// UnmarshalYAML reads a rate written as 25, 30000/1001 or 29.97.
func (r *Rate) UnmarshalYAML(n *yaml.Node) error {
	rate, err := parseRate(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*r = rate
	return nil
}

// MarshalYAML writes an integer rate as a number, and others as "Num/Den".
func (r Rate) MarshalYAML() (any, error) {
	if r.Den == 1 {
		return r.Num, nil
	}
	return r.String(), nil
}

// String returns the rate as in an MPD: "25" or "30000/1001".
func (r Rate) String() string {
	if r.Den == 1 {
		return strconv.Itoa(r.Num)
	}
	return fmt.Sprintf("%d/%d", r.Num, r.Den)
}

// label returns a short label like "25fps" or "29.97fps".
func (r Rate) label() string {
	if r.Den == 1 {
		return fmt.Sprintf("%dfps", r.Num)
	}
	return fmt.Sprintf("%.2ffps", float64(r.Num)/float64(r.Den))
}

// nominal returns the rate rounded up, like 30 for 29.97. Timecodes count
// frames in this base.
func (r Rate) nominal() int {
	return (r.Num + r.Den - 1) / r.Den
}

// dropFrames returns the number of frame numbers that a SMPTE drop-frame
// timecode skips every minute (except every tenth minute): 2 at 29.97 fps
// and 4 at 59.94 fps. It is 0 for other rates, which use a plain timecode.
func (r Rate) dropFrames() int {
	switch r {
	case Rate{30000, 1001}:
		return 2
	case Rate{60000, 1001}:
		return 4
	}
	return 0
}

// timescale returns the video timescale: 90000 when it fits integer rates,
// and otherwise the numerator, giving frame durations like 1001 at 29.97 fps.
func (r Rate) timescale() int {
	switch {
	case r.Den == 1 && 90000%r.Num == 0:
		return 90000
	case r.Den == 1:
		return r.Num * 1000
	}
	return r.Num
}

// frameTicks returns the frame duration in the video timescale.
func (r Rate) frameTicks() int {
	return r.timescale() * r.Den / r.Num
}

// frames converts seconds to a number of frames, and tells if it is exact.
func (r Rate) frames(seconds float64) (int, bool) {
	f := seconds * float64(r.Num) / float64(r.Den)
	n := math.Round(f)
	return int(n), math.Abs(f-n) < 1e-6
}

// loopSegments returns the smallest number of segments that is a whole
// number of milliseconds (as livesim2 counts the loop) and a whole number of
// audio frames for every audio codec of the config, so that the asset loops
// seamlessly. Asset durations should be multiples of it.
func (c *Config) loopSegments() int {
	r := c.Video.FPS
	sf := c.segFrames()
	// A segment is sf*Den/Num seconds. k segments must be whole for each check.
	k := 1
	whole := func(num, den int) { k = lcm(k, den/gcd(num, den)) }
	whole(sf*r.Den*1000, r.Num) // milliseconds
	for _, codec := range c.audioCodecs() {
		whole(sf*r.Den*audioRate, r.Num*aFrameSizes[codec]) // audio frames
	}
	return k
}

// audioCodecs returns the distinct audio codecs of the config, sorted.
func (c *Config) audioCodecs() []string {
	seen := make(map[string]bool)
	var codecs []string
	for _, a := range c.Audio.Representations {
		if !seen[a.Codec] {
			seen[a.Codec] = true
			codecs = append(codecs, a.Codec)
		}
	}
	sort.Strings(codecs)
	return codecs
}

// suggest returns a text with the exact asset durations: the shortest, and
// the ones nearest to target seconds.
func (c *Config) suggest(target float64) string {
	k := c.loopSegments()
	segDur := float64(c.segFrames()*c.Video.FPS.Den) / float64(c.Video.FPS.Num)
	loop := float64(k) * segDur
	what := "video frames and milliseconds"
	if codecs := c.audioCodecs(); len(codecs) > 0 {
		what += ", and audio frames of " + strings.Join(codecs, ", ")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Durations that are whole segments, %s, at %s fps with %g s segments:\n",
		what, c.Video.FPS, segDur)
	fmt.Fprintf(&b, "  shortest: %s\n", durText(loop, k))
	if target > 0 {
		m := max(int(target/loop), 1)
		var near []string
		for _, n := range []int{m, m + 1} {
			near = append(near, durText(float64(n)*loop, n*k))
		}
		fmt.Fprintf(&b, "  near %g s: %s\n", target, strings.Join(near, ", "))
	}
	return b.String()
}

func durText(seconds float64, segs int) string {
	s := strconv.FormatFloat(seconds, 'f', 3, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	unit := "segments"
	if segs == 1 {
		unit = "segment"
	}
	return fmt.Sprintf("%s s (%s, %d %s)", s, clock(seconds), segs, unit)
}

// clock formats seconds as H:MM:SS.sss or M:SS.sss.
func clock(seconds float64) string {
	ms := int(math.Round(seconds * 1000))
	h, m, s, frac := ms/3600000, ms/60000%60, ms/1000%60, ms%1000
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d.%03d", h, m, s, frac)
	}
	return fmt.Sprintf("%d:%02d.%03d", m, s, frac)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func lcm(a, b int) int {
	return a / gcd(a, b) * b
}
