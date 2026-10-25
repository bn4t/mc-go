package server

import (
	"context"
	"testing"
	"time"

	"mc-go/api/event"
	"mc-go/api/player"
)

func TestPlayerLifecycleLegacyAndTypedTopicsPreservePayloadContracts(t *testing.T) {
	for _, transport := range []string{"java", "avatar"} {
		t.Run(transport, func(t *testing.T) {
			s := simulationTestServer(t)
			t.Cleanup(s.Stop)
			streams := make(map[string]<-chan event.Event)
			for _, topic := range []string{event.TopicPlayerJoin, event.TopicPlayerLeave, event.TopicPlayerJoined, event.TopicPlayerLeft} {
				ch, stop := s.bus.Subscribe(topic, 2)
				defer stop()
				streams[topic] = ch
			}
			read := func(topic string) event.Event {
				t.Helper()
				select {
				case got := <-streams[topic]:
					if got.Topic != topic {
						t.Fatalf("topic %q received %q", topic, got.Topic)
					}
					return got
				case <-time.After(5 * time.Second):
					t.Fatalf("missing lifecycle topic %q", topic)
					return event.Event{}
				}
			}
			var id player.ID
			if transport == "java" {
				session, _ := enforceSession(t, s, "Compatible")
				id = session.ID()
			} else {
				connection, err := pluginAvatarCore(s).Admit(context.Background(), player.Profile{Name: "Compatible", UUID: offlineUUID("Compatible")}, player.Options{})
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				id = connection.ID()
			}
			checkLegacy := func(topic string) {
				t.Helper()
				payload, ok := read(topic).Data.(map[string]string)
				if !ok || len(payload) != 1 || payload["name"] != "Compatible" {
					t.Fatalf("API v1 %q payload changed: %#v", topic, payload)
				}
			}
			checkLegacy(event.TopicPlayerJoin)
			joined, ok := read(event.TopicPlayerJoined).Data.(event.PlayerJoined)
			if !ok || joined.Player.ID != id || joined.Player.Profile.Name != "Compatible" {
				t.Fatalf("typed admission lost identity: %+v", joined)
			}
			record := s.findPlayerByIDOwned(id)
			s.simulationAction(func() { s.retirePlayerOwned(record, "Compatibility test") })
			checkLegacy(event.TopicPlayerLeave)
			left, ok := read(event.TopicPlayerLeft).Data.(event.PlayerLeft)
			if !ok || left.Player.ID != id || left.Player != joined.Player || left.Reason != "Compatibility test" {
				t.Fatalf("typed retirement lost identity: %+v", left)
			}
			s.simulationAction(func() { s.retirePlayerOwned(record, "Duplicate") })
			for topic, stream := range streams {
				select {
				case extra := <-stream:
					t.Fatalf("topic %q mixed or duplicated lifecycle payloads: %+v", topic, extra)
				default:
				}
			}
		})
	}
}
