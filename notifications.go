package miniapollo

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// notifyItem is one element of the `notifications` query parameter
// (same wire format as real Apollo, v1 and v2).
type notifyItem struct {
	NamespaceName  string `json:"namespaceName"`
	NotificationID int64  `json:"notificationId"`
}

// handleNotifications implements Apollo's long-polling endpoint
// (GET /notifications/v2, plus the /notifications v1 alias):
//
//   - if any requested namespace is ahead of the client's id, respond 200
//     with that single changed namespace (real Apollo also answers one);
//   - otherwise hold the request until something is published or the
//     timeout (default 30s, `trigger` seconds on v2, clamped 1s..60s) lapses;
//     on timeout respond 404, meaning "nothing changed, poll again".
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	appID := q.Get("appId")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "missing appId")
		return
	}
	cluster := q.Get("cluster")
	if cluster == "" {
		cluster = "default" // Apollo defaults to "default" cluster
	}

	rawN := q.Get("notifications")
	if rawN == "" {
		writeError(w, http.StatusBadRequest, "missing notifications")
		return
	}
	var items []notifyItem
	if err := json.Unmarshal([]byte(rawN), &items); err != nil {
		writeError(w, http.StatusBadRequest, "invalid notifications: "+err.Error())
		return
	}
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "empty notifications")
		return
	}

	// v2 `trigger`: per-request hold time in seconds, like real Apollo.
	hold := s.pollTimeoutOf(q.Get("trigger"))
	deadline := time.Now().Add(hold)

	// Subscribe before the first diff check so no publish slip through
	// between "check" and "wait".
	s.mu.Lock()
	id, ch := s.subscribeLocked()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.unsubscribeLocked(id)
		s.mu.Unlock()
	}()

	for {
		if body, changed := s.notifyResponse(appID, cluster, items); changed {
			w.Header().Set("Content-Type", "application/json;charset=UTF-8")
			_, _ = w.Write(body)
			return
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			writeError(w, http.StatusNotFound, "no change") // Apollo 404: nothing changed
			return
		}
		select {
		case <-ch: // a publish happened (any namespace); re-diff
		case <-r.Context().Done(): // client hung up
			return
		case <-time.After(remain):
			writeError(w, http.StatusNotFound, "no change")
			return
		}
	}
}

// pollTimeoutOf resolves the hold time for one request.
// Caller must NOT hold the mutex (reads pollTimeout).
func (s *Server) pollTimeoutOf(trigger string) time.Duration {
	if trigger == "" {
		return s.currentPollTimeout()
	}
	sec, err := strconv.Atoi(trigger)
	if err != nil || sec < 1 {
		return s.currentPollTimeout()
	}
	d := time.Duration(sec) * time.Second
	if d < minTrigger {
		d = minTrigger
	}
	if d > maxTrigger {
		d = maxTrigger
	}
	return d
}

func (s *Server) currentPollTimeout() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pollTimeout
}

// notifyResponse returns the 200 body if the client is behind on some
// namespace, following Apollo's "answer the first changed one" rule.
// Never-published namespaces count as server id -1 (client's initial id
// is -1 too, so an absent namespace never triggers a notification).
func (s *Server) notifyResponse(appID, cluster string, items []notifyItem) (body []byte, changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range items {
		serverID := int64(-1)
		if st := s.getNamespaceLocked(appID, cluster, it.NamespaceName); st != nil {
			serverID = st.notificationID
		}
		if serverID != it.NotificationID {
			out := []notifyItem{{NamespaceName: it.NamespaceName, NotificationID: serverID}}
			body, err := json.Marshal(out)
			if err != nil {
				return nil, false
			}
			return body, true
		}
	}
	return nil, false
}

// ---- watcher registry ----

// subscribeLocked registers a long-poll waiter; needs Server.mu.
func (s *Server) subscribeLocked() (int64, chan struct{}) {
	s.watchSeq++
	id := s.watchSeq
	ch := make(chan struct{}, 1) // buffered: one pending wake is enough
	s.watchers[id] = ch
	return id, ch
}

// unsubscribeLocked removes a waiter; needs Server.mu.
func (s *Server) unsubscribeLocked(id int64) {
	delete(s.watchers, id)
}

// broadcastLocked wakes every waiter on any publish; needs Server.mu.
// A buffered wake is dropped if one is already pending: the woken client
// re-diffs everything against current state, so no update can be missed.
func (s *Server) broadcastLocked() {
	for _, ch := range s.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
