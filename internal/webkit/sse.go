package webkit

import (
	"net/http"
	"time"
)

// SSEFrame formats one Server-Sent Event.
func SSEFrame(kind string, data []byte) []byte {
	b := make([]byte, 0, len(kind)+len(data)+16)
	b = append(b, "event: "...)
	b = append(b, kind...)
	b = append(b, "\ndata: "...)
	b = append(b, data...)
	return append(b, "\n\n"...)
}

// ServeSSE streams the frames received on ch until the client goes away.
// Comments are sent every ping so proxies keep idle connections open, and
// browsers are asked to wait retry before reconnecting (thundering herd
// after a restart).
func ServeSSE(w http.ResponseWriter, r *http.Request, ch <-chan []byte, ping, retry time.Duration) {
	rcx := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("retry: " + itoa(retry.Milliseconds()) + "\n\n"))
	if err := rcx.Flush(); err != nil {
		return
	}
	t := time.NewTicker(ping)
	defer t.Stop()
	write := func(b []byte) bool {
		rcx.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := w.Write(b); err != nil {
			return false
		}
		return rcx.Flush() == nil
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case frame := <-ch:
			if !write(frame) {
				return
			}
		case <-t.C:
			if !write([]byte(": ping\n\n")) {
				return
			}
		}
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
