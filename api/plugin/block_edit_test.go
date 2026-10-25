package plugin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	blockregistry "mc-go/api/registry"
	"mc-go/api/worldview"
)

func editTestKey(t *testing.T, name string) blockregistry.StateKey {
	t.Helper()
	key, err := blockregistry.NewStateKey("minecraft:"+name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestBlockEditHooksOrderVetoAndImmutableFields(t *testing.T) {
	h := NewHooks()
	defer h.Close()
	proposal := BlockEdit{Origin: "plugin:writer", Dimension: "minecraft:the_nether", Position: worldview.Pos{X: 1, Y: 64}, Before: editTestKey(t, "stone"), Target: editTestKey(t, "dirt")}
	glass := editTestKey(t, "glass")
	var order []int
	h.OnBlockEdit(context.Background(), -1, func(_ context.Context, edit *BlockEdit) bool {
		order = append(order, 1)
		edit.Origin, edit.Dimension, edit.Position, edit.Before = "forged", "other", worldview.Pos{}, blockregistry.StateKey{}
		edit.Target = glass
		return true
	})
	veto := h.OnBlockEdit(context.Background(), 0, func(_ context.Context, edit *BlockEdit) bool {
		order = append(order, 2)
		if edit.Origin != proposal.Origin || edit.Dimension != proposal.Dimension || edit.Position != proposal.Position || edit.Before != proposal.Before || edit.Target != glass {
			t.Fatalf("policy identity or target chain changed: %+v", edit)
		}
		return false
	})
	h.OnBlockEdit(context.Background(), 0, func(context.Context, *BlockEdit) bool { order = append(order, 3); return true })
	result, accepted, err := h.BeforeBlockEdit(context.Background(), proposal)
	if accepted || err != nil || result != proposal || !reflect.DeepEqual(order, []int{1, 2}) {
		t.Fatalf("veto result=%+v accepted=%v err=%v order=%v", result, accepted, err, order)
	}
	veto()
	veto()
	order = nil
	result, accepted, err = h.BeforeBlockEdit(context.Background(), proposal)
	want := proposal
	want.Target = glass
	if !accepted || err != nil || result != want || !reflect.DeepEqual(order, []int{1, 3}) {
		t.Fatalf("modified result=%+v accepted=%v err=%v order=%v", result, accepted, err, order)
	}
}

func TestBlockEditHookCancellationAndScopedFault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := NewHooks()
	h.OnBlockEdit(ctx, 0, func(context.Context, *BlockEdit) bool { cancel(); return true })
	if _, accepted, err := h.BeforeBlockEdit(context.Background(), BlockEdit{}); accepted || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled subscription accepted decision: %v %v", accepted, err)
	}
	h.Close()
	if _, accepted, err := h.BeforeBlockEdit(context.Background(), BlockEdit{}); accepted || !errors.Is(err, ErrHooksClosed) {
		t.Fatalf("closed hooks accepted decision: %v %v", accepted, err)
	}
	api := newTestAPI()
	var retained API
	manager, err := NewManager(context.Background(), api, []Plugin{fixturePlugin{name: "block-policy", init: func(a API) error {
		retained = a
		a.Hooks().OnBlockEdit(context.Background(), 0, func(context.Context, *BlockEdit) bool { panic("private policy failure") })
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, accepted, err := api.hooks.BeforeBlockEdit(context.Background(), BlockEdit{})
	var fault *CallbackError
	if accepted || !errors.As(err, &fault) || fault.Plugin != "block-policy" || fault.Phase != "block edit hook" {
		t.Fatalf("unattributed fault: accepted=%v err=%v", accepted, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	retained.Hooks().OnBlockEdit(context.Background(), 0, func(context.Context, *BlockEdit) bool { t.Error("expired policy ran"); return false })
	if _, accepted, err := api.hooks.BeforeBlockEdit(context.Background(), BlockEdit{}); !accepted || err != nil {
		t.Fatalf("expired callback survived: %v %v", accepted, err)
	}
}
