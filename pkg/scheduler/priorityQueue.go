package scheduler

import (
	"container/heap"
	"time"
)

// DeviceDeadline is a lightweight entry in the priority queue.
// Only stores ID and deadline to keep the Scheduler memory-efficient.
type DeviceDeadline struct {
	DeviceID int64
	Deadline time.Time
}

// DeadlineQueue implements heap.Interface as a min-heap ordered by Deadline.
type DeadlineQueue []*DeviceDeadline

func (pq DeadlineQueue) Len() int { return len(pq) }

func (pq DeadlineQueue) Less(i, j int) bool {
	return pq[i].Deadline.Before(pq[j].Deadline)
}

func (pq DeadlineQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *DeadlineQueue) Push(x any) {
	*pq = append(*pq, x.(*DeviceDeadline))
}

func (pq *DeadlineQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // Avoid memory leak
	*pq = old[0 : n-1]
	return item
}

// Peek returns the item with minimum deadline without removing it.
// Returns nil if the queue is empty.
func (pq *DeadlineQueue) Peek() *DeviceDeadline {
	if len(*pq) == 0 {
		return nil
	}
	return (*pq)[0]
}

// PopExpired removes and returns all entries with deadline <= now.
// Returns entries in deadline order (earliest first).
func (pq *DeadlineQueue) PopExpired(now time.Time) []*DeviceDeadline {
	expired := make([]*DeviceDeadline, 0)
	for pq.Len() > 0 {
		item := pq.Peek()
		if item.Deadline.After(now) {
			break
		}
		expired = append(expired, heap.Pop(pq).(*DeviceDeadline))
	}
	return expired
}

// PushEntry adds a new entry to the queue.
func (pq *DeadlineQueue) PushEntry(deviceID int64, deadline time.Time) {
	heap.Push(pq, &DeviceDeadline{
		DeviceID: deviceID,
		Deadline: deadline,
	})
}

// InitQueue initializes the heap with a list of device IDs, all with deadline = now.
func (pq *DeadlineQueue) InitQueue(deviceIDs []int64, now time.Time) {
	*pq = make(DeadlineQueue, len(deviceIDs))
	for i, id := range deviceIDs {
		(*pq)[i] = &DeviceDeadline{
			DeviceID: id,
			Deadline: now,
		}
	}
	heap.Init(pq)
}

// PushBatch adds multiple entries efficiently.
// Uses heap.Init() after appending all items - O(n) total instead of O(k log n) for k individual pushes.
func (pq *DeadlineQueue) PushBatch(entries []*DeviceDeadline) {
	*pq = append(*pq, entries...)
	heap.Init(pq)
}
