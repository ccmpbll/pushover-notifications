package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ready is the work for a message that needs none.
func ready(m Message) work {
	return func(context.Context) (Message, bool) { return m, true }
}

type deliveries struct {
	mu    sync.Mutex
	calls []string
	fails []error
}

func (d *deliveries) deliver(_ context.Context, m Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.calls = append(d.calls, m.Title)

	if len(d.fails) == 0 {
		return nil
	}

	err := d.fails[0]
	d.fails = d.fails[1:]

	return err
}

func (d *deliveries) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.calls)
}

// lockedBuffer is a log sink a test can read while the outbox is still writing.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func runOutbox(t *testing.T, d *deliveries, waits ...time.Duration) (*outbox, *lockedBuffer) {
	t.Helper()

	return runOutboxWith(t, d, nil, waits...)
}

// runOutboxWith sets the outbox up with configure before it starts, so a test
// can change a limit without racing the goroutines that read it.
func runOutboxWith(t *testing.T, d *deliveries, configure func(*outbox), waits ...time.Duration) (*outbox, *lockedBuffer) {
	t.Helper()

	logs := &lockedBuffer{}

	o := newOutbox(d.deliver, slog.New(slog.NewTextHandler(logs, nil)))
	o.waits = waits

	if configure != nil {
		configure(o)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() { o.Run(ctx); close(done) }()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	return o, logs
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()

	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}

	t.Fatalf("timed out waiting for %s", what)
}

func TestAMessageIsDelivered(t *testing.T) {
	d := &deliveries{}
	o, _ := runOutbox(t, d, time.Millisecond)

	if !o.Put(ready(Message{Title: "one"})) {
		t.Fatal("put refused")
	}

	eventually(t, "delivery", func() bool { return d.count() == 1 })
}

func TestATemporaryFailureIsTriedAgainAndThenDelivered(t *testing.T) {
	d := &deliveries{fails: []error{&sendError{Temporary: true, Reason: "down"}, &sendError{Temporary: true, Reason: "down"}}}
	o, _ := runOutbox(t, d, time.Millisecond, time.Millisecond, time.Millisecond)

	o.Put(ready(Message{Title: "one"}))

	eventually(t, "three attempts", func() bool { return d.count() == 3 })
}

func TestAPermanentFailureIsNotTriedAgainAndTheNextMessageStillGoes(t *testing.T) {
	d := &deliveries{fails: []error{&sendError{Reason: "bad user"}}}
	o, logs := runOutbox(t, d, time.Millisecond, time.Millisecond)

	o.Put(ready(Message{Title: "one"}))
	o.Put(ready(Message{Title: "two"}))

	eventually(t, "both messages tried once", func() bool { return d.count() == 2 })

	if !strings.Contains(logs.String(), "bad user") {
		t.Errorf("the refusal was not logged: %s", logs.String())
	}
}

func TestItGivesUpAfterTheLastWaitAndMovesOn(t *testing.T) {
	always := &sendError{Temporary: true, Reason: "down"}
	d := &deliveries{fails: []error{always, always, always, always, always}}
	o, logs := runOutbox(t, d, time.Millisecond, time.Millisecond)

	o.Put(ready(Message{Title: "one"}))
	eventually(t, "three attempts", func() bool { return d.count() == 3 })

	o.Put(ready(Message{Title: "two"}))
	eventually(t, "the next message", func() bool { return d.count() >= 4 })

	if !strings.Contains(logs.String(), "gave up") {
		t.Errorf("giving up was not logged: %s", logs.String())
	}
}

func TestAnErrorThatIsNotASendErrorIsNotRetried(t *testing.T) {
	d := &deliveries{fails: []error{errors.New("not configured")}}
	o, _ := runOutbox(t, d, time.Millisecond, time.Millisecond)

	o.Put(ready(Message{Title: "one"}))

	eventually(t, "one attempt", func() bool { return d.count() == 1 })
	time.Sleep(30 * time.Millisecond)

	if d.count() != 1 {
		t.Errorf("%d attempts, want 1", d.count())
	}
}

func TestAFullQueueRefusesAndNeverBlocks(t *testing.T) {
	block := make(chan struct{})
	o := newOutbox(func(context.Context, Message) error { <-block; return nil }, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	defer close(block)

	accepted := 0

	done := make(chan struct{})

	go func() {
		for range queueSize + 5 {
			if o.Put(ready(Message{})) {
				accepted++
			}
		}

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Put blocked on a full queue")
	}

	if accepted != queueSize {
		t.Errorf("accepted %d, want %d", accepted, queueSize)
	}
}

func TestWorkThatMakesNoMessageSendsNothingAndTheNextOneStillGoes(t *testing.T) {
	d := &deliveries{}
	o, _ := runOutbox(t, d, time.Millisecond)

	o.Put(func(context.Context) (Message, bool) { return Message{Title: "nothing"}, false })
	o.Put(ready(Message{Title: "one"}))

	eventually(t, "the second message", func() bool { return d.count() == 1 })

	if d.calls[0] != "one" {
		t.Errorf("sent %v", d.calls)
	}
}

func TestSlowWorkIsCutOffAndTheMessageStillGoesWithWhatItHas(t *testing.T) {
	d := &deliveries{}
	o, _ := runOutboxWith(t, d, func(o *outbox) { o.limit = 20 * time.Millisecond }, time.Millisecond)

	o.Put(func(ctx context.Context) (Message, bool) {
		<-ctx.Done()

		return Message{Title: "late"}, true
	})

	eventually(t, "the message after the limit", func() bool { return d.count() == 1 })
}

// gate is a message that is stuck being sent until the test lets go.
type gate struct {
	open chan struct{}
	sent chan string
}

func newGate() *gate { return &gate{open: make(chan struct{}), sent: make(chan string, 16)} }

func (g *gate) deliver(ctx context.Context, m Message) error {
	g.sent <- m.Title

	select {
	case <-g.open:
	case <-ctx.Done():
	}

	return nil
}

func runGated(t *testing.T, g *gate, configure func(*outbox)) *outbox {
	t.Helper()

	o := newOutbox(g.deliver, slog.New(slog.NewTextHandler(&lockedBuffer{}, nil)))
	if configure != nil {
		configure(o)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() { o.Run(ctx); close(done) }()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	return o
}

func TestPreparingAMessageIsNotHeldUpBySendingTheOneBefore(t *testing.T) {
	g := newGate()
	o := runGated(t, g, nil)

	var ran atomic.Int32

	o.Put(ready(Message{Title: "stuck"}))
	<-g.sent // the first message is now being sent, and does not finish

	// A power reading for the print that began meanwhile has to be taken now.
	o.Put(func(context.Context) (Message, bool) { ran.Add(1); return Message{}, false })

	eventually(t, "the work while a send is stuck", func() bool { return ran.Load() == 1 })
	close(g.open)
}

func TestMessagesAreSentInTheOrderTheyWereQueuedWhateverOrderTheirWorkFinishes(t *testing.T) {
	d := &deliveries{}
	o, _ := runOutbox(t, d, time.Millisecond)

	o.Put(func(context.Context) (Message, bool) {
		time.Sleep(80 * time.Millisecond)
		return Message{Title: "first"}, true
	})
	o.Put(ready(Message{Title: "second"}))

	eventually(t, "both sent", func() bool { return d.count() == 2 })

	if d.calls[0] != "first" || d.calls[1] != "second" {
		t.Errorf("sent %v", d.calls)
	}
}

func TestWorkForSeveralMessagesRunsAtOnceUpToTheLimit(t *testing.T) {
	d := &deliveries{}
	o, _ := runOutboxWith(t, d, func(o *outbox) { o.prep = 2 }, time.Millisecond)

	var running, most atomic.Int32

	release := make(chan struct{})

	slow := func(context.Context) (Message, bool) {
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}

		<-release
		running.Add(-1)

		return Message{Title: "x"}, true
	}

	for range 3 {
		o.Put(slow)
	}

	eventually(t, "two at once", func() bool { return running.Load() == 2 })
	time.Sleep(30 * time.Millisecond)

	if running.Load() != 2 || most.Load() != 2 {
		t.Errorf("running %d, most %d: want exactly two at a time", running.Load(), most.Load())
	}

	close(release)
	eventually(t, "all three sent", func() bool { return d.count() == 3 })
}

func TestOnlyAFewPreparedMessagesWaitForASend(t *testing.T) {
	g := newGate()
	o := runGated(t, g, func(o *outbox) { o.ahead = 2 })

	var started atomic.Int32

	count := func(context.Context) (Message, bool) { started.Add(1); return Message{Title: "x"}, true }

	for range 8 {
		o.Put(count)
	}

	<-g.sent
	time.Sleep(50 * time.Millisecond)

	// One is being sent and two wait: with a send stuck, no more is prepared,
	// so a long outage does not pile up pictures in memory.
	if n := started.Load(); n != 3 {
		t.Errorf("%d pieces of work started, want 3 (one sending, two waiting)", n)
	}

	close(g.open)
}

func TestSeveralMessagesAreWorkedOnAtOnceByDefault(t *testing.T) {
	d := &deliveries{}
	o, _ := runOutbox(t, d, time.Millisecond)

	var arrived atomic.Int32

	all := make(chan struct{})

	rendezvous := func(ctx context.Context) (Message, bool) {
		if arrived.Add(1) == 3 {
			close(all)
		}

		select {
		case <-all:
		case <-time.After(2 * time.Second):
		}

		return Message{Title: "x"}, true
	}

	for range 3 {
		o.Put(rendezvous)
	}

	eventually(t, "three sent", func() bool { return d.count() == 3 })

	if arrived.Load() != 3 {
		t.Fatalf("%d arrived", arrived.Load())
	}

	select {
	case <-all:
	default:
		t.Fatal("the three were not in hand at the same time")
	}
}
