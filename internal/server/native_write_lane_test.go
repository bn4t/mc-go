package server

import (
	"sync"
	"testing"
)

func TestNativeWriteLaneSubmissionOrder(t *testing.T) {
	s := &Server{}
	gate := make(chan struct{})
	first, second, third := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var order []int
	appendResult := func(n int) { mu.Lock(); order = append(order, n); mu.Unlock() }
	s.submitNativeWriteOwned(first, func() { <-gate; appendResult(1) })
	s.submitNativeWriteOwned(second, func() { appendResult(2) })
	s.submitNativeWriteOwned(third, func() { appendResult(3) })
	select {
	case <-second:
		t.Fatal("later region write overtook blocked earlier snapshot")
	default:
	}
	if s.nativeSave != nil {
		t.Fatal("selective write became a full-world completion receipt")
	}
	close(gate)
	<-third
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("write order %v", order)
	}
}
