package sandbox

import (
	"context"
	"fmt"
	"sync"
)

// Slot is the unit of parallelism of a worker: one physical core owning a
// small set of logical boxes. Most jobs use box 0; sandboxed checkers,
// Communication managers and user processes use the others. Boxes never
// cross slots, so jobs cannot deadlock waiting for each other's boxes.
//
// Every run needs a freshly initialised isolate box: isolate creates the
// box's control group at --init and reuses it for every --run, so peak
// memory and OOM counters would otherwise leak from one run into the next.
// To keep the ~3 ms --cleanup/--init cycle off the critical path, each
// logical box is backed by two isolate boxes used alternately: while one
// runs, the other is re-initialised in the background (on the maintenance
// cores, not on the judging core).
type Slot struct {
	Index int
	Core  int
	iso   *Isolate
	// maint are the CPUs used for background box maintenance.
	maint []int

	mu    sync.Mutex
	boxes []*logicalBox
}

type logicalBox struct {
	ids  [2]int
	next int // index into ids/boxes of the box handed out next
	// ready[k] is closed when phys[k] has been (re)initialised.
	ready [2]chan struct{}
	phys  [2]*Box
	err   [2]error
}

// NewSlot prepares a slot. ids must contain 2 isolate box ids per logical box.
func NewSlot(iso *Isolate, index, core int, ids []int) *Slot {
	s := &Slot{Index: index, Core: core, iso: iso}
	for i := 0; i+1 < len(ids); i += 2 {
		s.boxes = append(s.boxes, &logicalBox{ids: [2]int{ids[i], ids[i+1]}})
	}
	return s
}

// SetMaintenanceCores sets the CPUs used for background re-initialisation.
func (s *Slot) SetMaintenanceCores(cpus []int) { s.maint = cpus }

// Isolate is the sandbox configuration of the slot.
func (s *Slot) Isolate() *Isolate { return s.iso }

// NumBoxes returns how many logical boxes the slot owns.
func (s *Slot) NumBoxes() int { return len(s.boxes) }

func (s *Slot) initAsync(lb *logicalBox, k int) {
	ch := make(chan struct{})
	lb.ready[k] = ch
	old := lb.phys[k]
	go func() {
		defer close(ch)
		if old != nil {
			old.dispose()
		}
		b, err := s.iso.init(context.Background(), lb.ids[k], s.Core, s.maint)
		lb.phys[k], lb.err[k] = b, err
	}()
}

// Box returns logical box i, freshly initialised. The box previously
// returned for i must no longer be in use: it is recycled in the background.
func (s *Slot) Box(ctx context.Context, i int) (*Box, error) {
	if i < 0 || i >= len(s.boxes) {
		return nil, fmt.Errorf("slot %d has no box %d", s.Index, i)
	}
	s.mu.Lock()
	lb := s.boxes[i]
	k := lb.next
	if lb.ready[k] == nil {
		s.initAsync(lb, k)
	}
	ch := lb.ready[k]
	s.mu.Unlock()

	select {
	case <-ch:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if lb.err[k] != nil {
		err := lb.err[k]
		// Retry from scratch next time.
		lb.ready[k], lb.phys[k], lb.err[k] = nil, nil, nil
		return nil, err
	}
	b := lb.phys[k]
	// Hand out k; recycle the other one (the previous user is done with it)
	// so it is ready for the next call.
	other := 1 - k
	if lb.phys[other] != nil || lb.ready[other] == nil {
		s.initAsync(lb, other)
	}
	// Mark k as consumed: it must be re-initialised before reuse.
	lb.next = other
	lb.ready[k] = nil
	return b, nil
}

// Recycle discards every box of the slot (used after sandbox errors); they
// are re-created on next use.
func (s *Slot) Recycle(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, lb := range s.boxes {
		for k := 0; k < 2; k++ {
			if ch := lb.ready[k]; ch != nil {
				<-ch
			}
			if lb.phys[k] != nil {
				lb.phys[k].dispose()
			}
			lb.phys[k], lb.ready[k], lb.err[k] = nil, nil, nil
		}
	}
}

// Close destroys every box of the slot.
func (s *Slot) Close(ctx context.Context) { s.Recycle(ctx) }

// BoxesPerSlot is the number of logical boxes per slot: the program, a
// checker/manager, a checker compiler and user processes of Communication
// tasks.
const BoxesPerSlot = 6

// NewSlots creates one slot per core; isolate box ids start at offset
// (2 per logical box). maint lists the CPUs not used by any slot.
func NewSlots(iso *Isolate, cores []int, offset int, maint []int) []*Slot {
	slots := make([]*Slot, len(cores))
	for i, c := range cores {
		ids := make([]int, 2*BoxesPerSlot)
		for j := range ids {
			ids[j] = offset + i*2*BoxesPerSlot + j
		}
		slots[i] = NewSlot(iso, i, c, ids)
		slots[i].SetMaintenanceCores(maint)
	}
	return slots
}
