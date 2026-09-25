// Package events carries live notifications (submission status changes,
// announcements, messages, question replies) from the backend services to
// the web servers, which forward them to browsers over SSE.
//
// Notifications go through Redis pub/sub: fire-and-forget and cheap. Every
// web node subscribes once and fans events out in memory; clients re-sync
// their state on reconnect, so a missed event only delays an update.
package events

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

// Event types.
const (
	TypeSubmission   = "submission"
	TypeUserTest     = "user_test"
	TypeAnnouncement = "announcement"
	TypeMessage      = "message"
	TypeQuestion     = "question"     // a reply to a contestant's question
	TypeQuestionNew  = "question_new" // for admins
	TypeAlert        = "alert"        // system errors, for admins
	TypeContest      = "contest"      // contest settings changed (caches)
	TypeBalloon      = "balloon"      // a first accepted submission, for admins
	TypePrint        = "print"        // a print job was queued or printed
)

// Event is a notification. ParticipationID 0 with a ContestID means every
// participant of the contest.
type Event struct {
	Type            string `json:"type"`
	ContestID       int64  `json:"contest_id,omitempty"`
	ParticipationID int64  `json:"participation_id,omitempty"`
	TaskID          int64  `json:"task_id,omitempty"`
	SubmissionID    int64  `json:"submission_id,omitempty"`
	UserTestID      int64  `json:"user_test_id,omitempty"`
	// Status of a submission: compiling, compilation_failed, evaluating,
	// scored, error. For user tests: compiling, running, done, error.
	Status string `json:"status,omitempty"`
	Done   int32  `json:"done,omitempty"`
	Total  int32  `json:"total,omitempty"`
	// Free-form text (alerts, subjects).
	Text string `json:"text,omitempty"`
}

// Channel returns the pub/sub channel of a namespace.
func Channel(ns string) string { return ns + "events" }

// Publish sends an event.
func Publish(ctx context.Context, rdb *redis.Client, ns string, e Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return rdb.Publish(ctx, Channel(ns), data).Err()
}

// Subscribe delivers events to fn until ctx ends. It reconnects
// automatically (go-redis resubscribes on reconnection).
func Subscribe(ctx context.Context, rdb *redis.Client, ns string, fn func(Event)) error {
	sub := rdb.Subscribe(ctx, Channel(ns))
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case m, ok := <-ch:
			if !ok {
				return nil
			}
			var e Event
			if json.Unmarshal([]byte(m.Payload), &e) == nil {
				fn(e)
			}
		}
	}
}
