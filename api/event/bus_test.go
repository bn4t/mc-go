package event

import (
	"context"
	"sync"
	"testing"
)

func TestCloseRacesPublishAndUnsubscribe(t *testing.T) {
	b := New()
	ch, unsubscribe := b.Subscribe("x", 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			b.Publish("x", 1)
		}
	})
	wg.Go(func() { unsubscribe(); unsubscribe() })
	wg.Go(func() { b.Close(); b.Close() })
	wg.Wait()
	// A channel unsubscribed before Close intentionally remains open.
	_ = ch
	closed, _ := b.Subscribe("x", 1)
	if _, ok := <-closed; ok {
		t.Fatal("closed bus accepted subscription")
	}
	b.Publish("x", 2)
}

func TestScopeCannotPublishOrSubscribeAfterCancellation(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	scoped := b.WithContext(ctx)
	cancel()
	ch, unsubscribe := b.Subscribe("x", 1)
	defer unsubscribe()
	scoped.WithContext(context.Background()).Publish("x", 1)
	select {
	case <-ch:
		t.Fatal("expired scope published")
	default:
	}
	expired, _ := scoped.Subscribe("x", 1)
	if _, ok := <-expired; ok {
		t.Fatal("expired scope subscribed")
	}
	scoped.Close()
	b.Publish("x", 2)
	if (<-ch).Data != 2 {
		t.Fatal("scoped Close affected root")
	}
}
