// Package room is the global clock. It is pure logic with no I/O so the sync
// rules can be tested exactly.
//
// The room is described by one anchor: "at server time Anchor (ms) the video
// is at Pos seconds, and it advances at Rate if Playing". Every client derives
// the same position from that anchor plus its own clock offset, so the server
// never streams time ticks.
package room

import "math"

const (
	ReasonNone         = ""
	ReasonHost         = "host"
	ReasonIntermission = "intermission"
	ReasonBuffering    = "buffering"
	ReasonEncoder      = "encoder"
	ReasonEnded        = "ended"
	ReasonLoading      = "loading"
)

type State struct {
	MediaID    string  `json:"media"`
	Playing    bool    `json:"playing"`
	Pos        float64 `json:"pos"`
	Anchor     int64   `json:"anchor"`
	Rate       float64 `json:"rate"`
	Reason     string  `json:"reason"`
	By         string  `json:"by,omitempty"` // display name of whoever caused the last change
	InterUntil int64   `json:"interUntil,omitempty"`
	Version    uint64  `json:"v"`
}

// PositionAt returns the room position at server time ms.
func (s State) PositionAt(ms int64) float64 {
	if !s.Playing || ms <= s.Anchor {
		return s.Pos
	}
	return s.Pos + float64(ms-s.Anchor)/1000*s.Rate
}

type Room struct {
	st       State
	duration float64 // 0 = unknown
	limit    float64 // furthest playable second while encoding; 0 = no limit
}

func New() *Room { return &Room{st: State{Rate: 1, Reason: ReasonLoading}} }

func (r *Room) State() State { return r.st }

// Load switches media. The room starts paused at zero.
func (r *Room) Load(now int64, mediaID string, duration float64, by string) State {
	r.st = State{MediaID: mediaID, Rate: 1, Anchor: now, Reason: ReasonLoading, By: by, Version: r.st.Version}
	r.duration = duration
	r.limit = 0
	return r.bump()
}

// Clear stops the video for everyone and leaves the room without one.
func (r *Room) Clear(now int64, by string) State {
	r.st = State{Rate: 1, Anchor: now, Reason: ReasonLoading, By: by, Version: r.st.Version}
	r.duration, r.limit = 0, 0
	return r.bump()
}

// SetPlayable tells the room how far the encoder has got (seconds) and the
// final duration. limit < 0 means fully encoded.
func (r *Room) SetPlayable(duration, limit float64) {
	if duration > 0 {
		r.duration = duration
	}
	if limit < 0 {
		r.limit = 0
	} else {
		r.limit = limit
	}
}

// Ready clears the loading reason once media can be played.
func (r *Room) Ready(now int64) (State, bool) {
	if r.st.Reason != ReasonLoading {
		return r.st, false
	}
	r.st.Reason = ReasonHost
	r.st.Anchor = now
	return r.bump(), true
}

// Play starts the room `lead` ms in the future so every viewer begins on the
// same tick.
func (r *Room) Play(now, lead int64, by string) (State, bool) {
	if r.st.MediaID == "" || r.st.Reason == ReasonLoading {
		return r.st, false
	}
	if r.st.Playing {
		return r.st, false
	}
	pos := r.clamp(r.st.Pos)
	if r.duration > 0 && pos >= r.duration-0.5 {
		pos = 0 // replay from the start after the end
	}
	r.st.Pos = pos
	r.st.Playing = true
	r.st.Anchor = now + lead
	r.st.Reason = ReasonNone
	r.st.InterUntil = 0
	r.st.By = by
	return r.bump(), true
}

// Pause freezes the room where it is right now.
func (r *Room) Pause(now int64, reason, by string) (State, bool) {
	if !r.st.Playing && reason != ReasonIntermission {
		if r.st.Reason == reason {
			return r.st, false
		}
		r.st.Reason = reason
		r.st.By = by
		return r.bump(), true
	}
	r.st.Pos = r.clamp(r.st.PositionAt(now))
	r.st.Playing = false
	r.st.Anchor = now
	r.st.Reason = reason
	r.st.By = by
	return r.bump(), true
}

// Seek moves everyone. If the room was playing it keeps playing after a lead.
func (r *Room) SeekTo(now, lead int64, pos float64, by string) (State, bool) {
	if r.st.MediaID == "" || math.IsNaN(pos) || math.IsInf(pos, 0) {
		return r.st, false
	}
	r.st.Pos = r.clamp(pos)
	r.st.By = by
	if r.st.Playing {
		r.st.Anchor = now + lead
	} else {
		r.st.Anchor = now
	}
	return r.bump(), true
}

// Intermission pauses everyone with a countdown. seconds <= 0 means open ended.
func (r *Room) Intermission(now int64, seconds int, by string) (State, bool) {
	r.Pause(now, ReasonIntermission, by)
	if seconds > 0 {
		r.st.InterUntil = now + int64(seconds)*1000
	} else {
		r.st.InterUntil = 0
	}
	return r.bump(), true
}

// ExtendIntermission adds time to the countdown.
func (r *Room) ExtendIntermission(now int64, seconds int) (State, bool) {
	if r.st.Reason != ReasonIntermission {
		return r.st, false
	}
	base := r.st.InterUntil
	if base < now {
		base = now
	}
	r.st.InterUntil = base + int64(seconds)*1000
	return r.bump(), true
}

// Tick applies time-based rules. It returns the new state and whether it
// changed. waitForEncoder is the number of seconds of headroom wanted.
func (r *Room) Tick(now, lead int64) (State, bool) {
	s := r.st
	switch {
	case s.Reason == ReasonIntermission && s.InterUntil > 0 && now >= s.InterUntil:
		r.st.Reason = ReasonHost
		return r.Play(now, lead, "Intermission over")
	case s.Playing && r.duration > 0 && s.PositionAt(now) >= r.duration:
		r.st.Pos = r.duration
		r.st.Playing = false
		r.st.Anchor = now
		r.st.Reason = ReasonEnded
		return r.bump(), true
	case s.Playing && r.limit > 0 && s.PositionAt(now) >= r.limit-6:
		return r.Pause(now, ReasonEncoder, "Encoder")
	case !s.Playing && s.Reason == ReasonEncoder && (r.limit == 0 || r.limit >= s.Pos+20):
		r.st.Reason = ReasonHost
		return r.Play(now, lead, "Encoder caught up")
	}
	return s, false
}

// ResumeAfterBuffering restarts the room after a wait-for-everyone stall.
func (r *Room) ResumeAfterBuffering(now, lead int64) (State, bool) {
	if r.st.Playing || r.st.Reason != ReasonBuffering {
		return r.st, false
	}
	r.st.Reason = ReasonHost
	return r.Play(now, lead, "Everyone buffered")
}

func (r *Room) clamp(pos float64) float64 {
	if pos < 0 {
		pos = 0
	}
	max := r.duration
	if r.limit > 0 && (max == 0 || r.limit-2 < max) {
		max = r.limit - 2
		if max < 0 {
			max = 0
		}
	}
	if max > 0 && pos > max {
		pos = max
	}
	return pos
}

func (r *Room) bump() State {
	r.st.Version++
	return r.st
}

// Lead computes how far in the future a start is scheduled, from the worst
// round trip among following viewers. The cap keeps one bad link from making
// everyone wait.
func Lead(worstRTTms int64) int64 {
	if worstRTTms > 1200 {
		worstRTTms = 1200
	}
	lead := 2*worstRTTms + 400
	if lead < 700 {
		lead = 700
	}
	if lead > 3000 {
		lead = 3000
	}
	return lead
}
