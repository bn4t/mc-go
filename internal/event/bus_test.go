package event

import "testing"

func TestPublishSubscribe(t *testing.T) {
	b := New()
	ch, unsub := b.Subscribe(TopicPlayerJoin, 4)
	defer unsub()
	b.Publish(TopicPlayerJoin, "steve")
	select {
	case e := <-ch:
		if e.Data != "steve" {
			t.Fatalf("data = %v", e.Data)
		}
	default:
		t.Fatal("no event delivered")
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := New()
	ch, unsub := b.Subscribe(TopicChat, 4)
	unsub()
	b.Publish(TopicChat, "hi")
	select {
	case e := <-ch:
		t.Fatalf("unexpected event %v", e)
	default:
	}
}

func TestSlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	b := New()
	ch, unsub := b.Subscribe(TopicTick, 1)
	defer unsub()
	b.Publish(TopicTick, 1)
	b.Publish(TopicTick, 2) // buffer full: must drop, not block
	b.Publish(TopicTick, 3)
	if first := <-ch; first.Data != 1 {
		t.Fatalf("first = %v", first.Data)
	}
}
