package main

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// queueSize is how many messages wait to be sent. A print ending is rare, so
// a full queue means Pushover has been unreachable for a long time.
const queueSize = 32

// retryWaits is how long to wait before each new try of a message that failed
// for a reason that may pass. A message is tried len(retryWaits)+1 times.
//
//nolint:gochecknoglobals // a constant list.
var retryWaits = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second, 2 * time.Minute}

// workLimit is how long the work behind one message may take: the picture
// fetches, the power readings and the host's own answers.
const workLimit = 45 * time.Second

// work is what a message needs doing before it can be sent. It is run in the
// outbox, apart from the event stream, because it asks other servers for
// things (a picture from Connect, the power from the plugs) and any of them can
// be slow. It answers the message, or false when there is nothing to send. The
// queue holds these and not finished messages, so a queue of messages with
// pictures costs no memory until each is sent.
type work func(ctx context.Context) (Message, bool)

// outbox does the work for messages in parallel, a few at a time, and sends the
// messages one at a time in the order they were queued, apart from the event
// stream. Doing the work and sending are separate on purpose: a send that is
// being retried for minutes must not hold up the next print's power reading,
// which has to be taken when the print starts.
type outbox struct {
	queue   chan work
	deliver func(context.Context, Message) error
	log     *slog.Logger
	waits   []time.Duration
	limit   time.Duration
	// prep is how many pieces of work run at once, and ahead is how many
	// finished or unfinished messages may wait for the one being sent. Together
	// they bound the pictures held in memory while Pushover is unreachable.
	prep  int
	ahead int
}

const (
	prepLimit  = 4
	aheadLimit = 8
)

// slot is one message on its way: its work, then its send, in queue order.
type slot struct {
	done chan struct{}
	m    Message
	ok   bool
}

func newOutbox(deliver func(context.Context, Message) error, log *slog.Logger) *outbox {
	return &outbox{
		queue: make(chan work, queueSize), deliver: deliver, log: log,
		waits: retryWaits, limit: workLimit, prep: prepLimit, ahead: aheadLimit,
	}
}

// Put queues work. It never blocks: false means the queue is full.
func (o *outbox) Put(w work) bool {
	select {
	case o.queue <- w:
		return true
	default:
		return false
	}
}

// Run starts each piece of work as it is queued, and sends what they make in
// order, until ctx ends. A piece of work is started before the one queued after
// it, so a print's start work is always running before its end work.
func (o *outbox) Run(ctx context.Context) {
	slots := make(chan *slot, o.ahead)
	running := make(chan struct{}, o.prep)

	go o.sender(ctx, slots)

	for {
		select {
		case <-ctx.Done():
			return
		case w := <-o.queue:
			s := &slot{done: make(chan struct{})}

			// Both waits are here and not in the goroutine, so work starts in
			// queue order and no more than o.prep+o.ahead messages are in hand.
			select {
			case slots <- s:
			case <-ctx.Done():
				return
			}

			select {
			case running <- struct{}{}:
			case <-ctx.Done():
				return
			}

			go func() {
				defer func() {
					<-running
					close(s.done)
				}()

				workCtx, cancel := context.WithTimeout(ctx, o.limit)
				defer cancel()

				s.m, s.ok = w(workCtx)
			}()
		}
	}
}

// sender sends the messages in the order they were queued, waiting for each
// one's work to finish.
func (o *outbox) sender(ctx context.Context, slots <-chan *slot) {
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-slots:
			select {
			case <-s.done:
			case <-ctx.Done():
				return
			}

			if s.ok {
				o.send(ctx, s.m)
			}
		}
	}
}

func (o *outbox) send(ctx context.Context, m Message) {
	for attempt := 0; ; attempt++ {
		err := o.deliver(ctx, m)
		if err == nil {
			o.log.InfoContext(ctx, "notification sent", "title", m.Title)

			return
		}

		failed, ok := errors.AsType[*sendError](err)
		if !ok || !failed.Temporary {
			o.log.WarnContext(ctx, "notification not sent", "title", m.Title, "error", err)

			return
		}

		if attempt >= len(o.waits) {
			o.log.WarnContext(ctx, "gave up sending a notification", "title", m.Title, "attempts", attempt+1, "error", err)

			return
		}

		o.log.WarnContext(ctx, "notification not sent, trying again", "title", m.Title, "wait", o.waits[attempt], "error", err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(o.waits[attempt]):
		}
	}
}
