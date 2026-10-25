package externalplugin

import (
	"context"
	"testing"

	"mc-go/api/event"
	"mc-go/api/player"
	"mc-go/api/plugin"
	_ "mc-go/plugins/anticheat"
	_ "mc-go/plugins/hello"
)

type extension struct{}

func (extension) Name() string              { return "external" }
func (extension) Metadata() plugin.Metadata { return plugin.Metadata{APIVersion: plugin.APIVersion} }
func (extension) Init(api plugin.API) error {
	api.Hooks().OnAdmission(api.Context(), 0, func(_ context.Context, profile player.Profile) plugin.AdmissionDecision {
		return plugin.AdmissionDecision{Denied: profile.Name == "Blocked", Reason: "Admission policy"}
	})
	if catalog := api.BlockCatalog(); catalog != nil {
		state, ok := catalog.Resolve("minecraft:air", nil)
		if ok {
			_, _ = catalog.State(state)
		}
	}
	api.Hooks().OnMove(api.Context(), 0, func(ctx context.Context, proposal *plugin.Move) bool {
		if proposal.To.Y < 0 {
			return false
		}
		proposal.To.X = 0
		// Enforcement is queued, so calling it from this hook does not reenter
		// the action owner or split this movement's authoritative commit.
		if controls, ok := api.(plugin.PlayerControl); ok {
			controls.TeleportID(proposal.ID, 0, 65, 0)
		}
		return true
	})
	api.Bus().SubscribeContext(api.Context(), event.TopicPlayerMove, 16)
	return nil
}

var _ plugin.Plugin = extension{}
var _ plugin.Described = extension{}

func TestPublicPluginContract(t *testing.T) {
	h := plugin.NewHooks()
	h.OnMove(context.Background(), 0, func(_ context.Context, move *plugin.Move) bool { move.To.Y = 65; return true })
	result, ok := h.BeforeMove(context.Background(), plugin.Move{To: plugin.Position{Y: 1}})
	if !ok || result.To.Y != 65 {
		t.Fatal("public hook failed")
	}
	h.Close()
}
