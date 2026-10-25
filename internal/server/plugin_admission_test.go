package server

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/api/event"
	"mc-go/api/player"
	"mc-go/api/plugin"
	"mc-go/internal/protocol"
)

func rejectedJavaPolicyLogin(t *testing.T, s *Server, name string) string {
	t.Helper()
	client, peer := net.Pipe()
	done := make(chan struct{})
	go func() { s.handleConn(peer); close(done) }()
	defer func() { client.Close(); <-done }()
	if err := client.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	r, w := protocol.NewReader(client), protocol.NewWriter(client)
	if err := sendHandshake(w, ProtocolVersion, "localhost", 25565, NextLogin); err != nil {
		t.Fatal(err)
	}
	var login protocol.Buffer
	login.String(name)
	login.Raw(make([]byte, 16))
	if err := w.WritePacket(IDLoginStart, login.Bytes()); err != nil {
		t.Fatal(err)
	}
	packet, err := r.ReadPacket()
	if err != nil || packet.ID != IDLoginDisconnect {
		t.Fatalf("policy rejected login produced %#x, %v", packet.ID, err)
	}
	encoded, err := protocol.NewPacketReader(packet.Data).String()
	if err != nil {
		t.Fatal(err)
	}
	var reason map[string]string
	if err := json.Unmarshal([]byte(encoded), &reason); err != nil {
		t.Fatal(err)
	}
	return reason["text"]
}

func TestPluginAdmissionPoliciesGuardJavaAndNeutral(t *testing.T) {
	for _, mode := range []string{"deny", "panic", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s := simulationTestServer(t)
			t.Cleanup(s.Stop)
			joined, stopJoin := s.bus.Subscribe(event.TopicPlayerJoin, 4)
			defer stopJoin()
			left, stopLeft := s.bus.Subscribe(event.TopicPlayerLeave, 4)
			defer stopLeft()
			install := func() func() {
				lifetime, cancel := context.WithCancel(context.Background())
				unsubscribe := s.hooks.OnAdmission(lifetime, 0, func(_ context.Context, profile player.Profile) plugin.AdmissionDecision {
					if profile.Name != "PolicyGuard" || profile.UUID != offlineUUID("PolicyGuard") {
						t.Error("policy did not receive authenticated copied identity")
					}
					switch mode {
					case "panic":
						panic("private admission detail")
					case "cancel":
						cancel()
						return plugin.AdmissionDecision{}
					default:
						return plugin.AdmissionDecision{Denied: true, Reason: "Maintenance window"}
					}
				})
				return func() { unsubscribe(); cancel() }
			}
			uninstall := install()
			reason := rejectedJavaPolicyLogin(t, s, "PolicyGuard")
			uninstall()
			if mode == "deny" && !strings.Contains(reason, "Maintenance window") {
				t.Fatalf("intentional denial reason lost: %q", reason)
			}
			if mode == "panic" && (strings.Contains(reason, "private") || !strings.Contains(reason, "Admission policy failed")) {
				t.Fatalf("unsafe panic response: %q", reason)
			}
			uninstall = install()
			connection, err := pluginAvatarCore(s).Admit(context.Background(), player.Profile{Name: "PolicyGuard", UUID: offlineUUID("PolicyGuard")}, player.Options{})
			uninstall()
			if err == nil || connection != nil {
				t.Fatalf("neutral admission bypassed policy: %v %v", connection, err)
			}
			if s.OnlineCount() != 0 || len(s.activePlayersOwned()) != 0 || len(s.entities.All()) != 0 {
				t.Fatalf("rejection mutated lifecycle: online=%d players=%d entities=%d", s.OnlineCount(), len(s.activePlayersOwned()), len(s.entities.All()))
			}
			for name, stream := range map[string]<-chan event.Event{"join": joined, "leave": left} {
				select {
				case observed := <-stream:
					t.Fatalf("rejected policy emitted %s: %+v", name, observed)
				default:
				}
			}
		})
	}
}

func TestPluginAdmissionVetoPreservesExistingJavaIncarnation(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	old, _ := enforceSession(t, s, "Protected")
	x, y, z, _, _, _ := old.Pos()
	entity := s.entities.Spawn(&Entity{UUID: old.getUUID(), Type: mustEntityType("player"), Dim: dimOverworld, X: x, Y: y, Z: z})
	old.entityID.Store(entity.ID)
	before, _ := s.playerStateByID(old.ID())
	remove := s.hooks.OnAdmission(context.Background(), 0, func(context.Context, player.Profile) plugin.AdmissionDecision {
		return plugin.AdmissionDecision{Denied: true, Reason: "Replacement refused"}
	})
	defer remove()
	if got := rejectedJavaPolicyLogin(t, s, "Protected"); !strings.Contains(got, "Replacement refused") {
		t.Fatalf("replacement policy response=%q", got)
	}
	after, found := s.playerStateByID(old.ID())
	if !found || after != before || old.closing.Load() || s.OnlineCount() != 1 {
		t.Fatalf("denied replacement changed admitted actor: %+v %v online=%d", after, found, s.OnlineCount())
	}
	if _, found := s.entities.Get(entity.ID); !found {
		t.Fatal("denied replacement removed existing entity")
	}
}
