package server

import (
	"strings"

	"mc-go/internal/nbt"
)

func ordinaryVehicle(name string) bool {
	if _, registered := EntityTypeID(name); !registered {
		return false
	}
	return isBoatEntityName(name) || isMinecartName(name) && name != "command_block_minecart" && name != "spawner_minecart"
}

// nativePassengerTreeEntity matches the ordinary/generic restore branches.
// Special roots such as dragons and command carts still save their supported
// children independently until their own loaders restore passenger trees.
func nativePassengerTreeEntity(e *Entity) bool {
	name, registered := EntityTypeName(e.Type)
	return registered && (e.Living || ordinaryVehicle(name)) && nativePassengerTreeName(name)
}

func nativePassengerTreeName(name string) bool {
	if _, registered := EntityTypeID(name); !registered || name == "ender_dragon" {
		return false
	}
	if ordinaryVehicle(name) {
		return true
	}
	if _, known := mobHealth[name]; known {
		return true
	}
	return entityAttrBases[name]["max_health"] > 0
}

func nativePassengerListed(parent *Entity, id int32) bool {
	for _, passenger := range parent.Passengers {
		if passenger == id {
			return true
		}
	}
	return false
}

func vehicleContainerSize(name string) int {
	if name == "hopper_minecart" {
		return 5
	}
	if name == "chest_minecart" || isBoatEntityName(name) && (strings.HasSuffix(name, "_chest_boat") || name == "bamboo_chest_raft") {
		return 27
	}
	return 0
}

// vehicleLoadFields reads ContainerEntity cargo and the furnace/hopper/TNT
// subtype state, independently of the shared living-mob entity loader.
func vehicleLoadFields(e *Entity, name string, save map[string]any) {
	if !ordinaryVehicle(name) {
		return
	}
	if e.ExtraNBT == nil {
		e.ExtraNBT = map[string]any{}
	}
	fields := e.ExtraNBT
	if isMinecartName(name) {
		e.CartFlipped = nbtTruth(save["FlippedRotation"])
		fields["HasTicked"] = nbtBool(nbtTruth(save["HasTicked"]))
	}
	if size := vehicleContainerSize(name); size > 0 {
		delete(fields, "Items")
		delete(fields, "LootTable")
		delete(fields, "LootTableSeed")
		if table, ok := save["LootTable"].(string); ok && table != "" {
			fields["LootTable"] = table
			if seed, ok := nbtNumber(save["LootTableSeed"]); ok && seed != 0 {
				fields["LootTableSeed"] = seed
			}
		} else {
			slots := make([]ItemStack, size)
			itemsInto(save["Items"], slots)
			items := nbt.List{Elem: nbt.TagCompound}
			for slot, item := range slots {
				if !item.Empty() {
					items.Items = append(items.Items, itemNBTSlotted(item, int8(slot)))
				}
			}
			fields["Items"] = items
		}
	}
	switch name {
	case "hopper_minecart":
		fields["Enabled"] = int8(1)
		if value, ok := save["Enabled"]; ok {
			fields["Enabled"] = nbtBool(nbtTruth(value))
		}
	case "furnace_minecart":
		fields["Fuel"] = int16(nbtNumberOr(save["Fuel"], 0))
		for _, key := range []string{"PushX", "PushZ"} {
			fields[key] = float64(0)
			if value, ok := vehicleNativeDouble(save[key]); ok {
				fields[key] = value
			}
		}
	case "tnt_minecart":
		fields["fuse"] = nbtNumberOr(save["fuse"], -1)
	}
}

func vehicleSavedFields(e *Entity, name string, save map[string]any) {
	if !ordinaryVehicle(name) {
		return
	}
	if e.CustomName != "" {
		save["CustomName"] = commandMinecartOutput(storedComponentArg(e.CustomName, e.CustomNameComp))
	}
	if isMinecartName(name) {
		save["FlippedRotation"] = nbtBool(e.CartFlipped)
	}
	vehicleCargoSavedFields(e, save)
	if vehicleContainerSize(name) > 0 {
		if _, loot := save["LootTable"]; loot {
			delete(save, "Items")
		} else if _, items := save["Items"]; !items {
			save["Items"] = nbt.List{Elem: nbt.TagCompound}
		}
	}
}

func vehicleNativeDouble(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	default:
		if n, ok := nbtNumber(value); ok {
			return float64(n), true
		}
	}
	return 0, false
}

// restoreVehicleNative constructs vehicles directly; summonMobRaw accepts only
// living types. Passenger trees are restored once beneath their saved root.
func (s *Server) restoreVehicleNative(d *dimension, name string, save map[string]any, pos [3]float64, uuid [16]byte) *Entity {
	id, registered := EntityTypeID(name)
	if !registered {
		return nil
	}
	e := s.entities.Spawn(&Entity{Type: id, Dim: d.name, X: pos[0], Y: pos[1], Z: pos[2]})
	if uuid != [16]byte{} {
		e.UUID = uuid
	}
	s.applyEntitySave(e, save)
	e.CartYawO = e.Yaw
	e.dataSync = nil
	e.MetaDirty = false
	s.restoreNativePassengers(d, e, save)
	return e
}

func (s *Server) restoreNativePassengers(d *dimension, parent *Entity, save map[string]any) {
	type treeNode struct {
		parent *Entity
		save   map[string]any
	}
	pending := []treeNode{{parent, save}}
	for cursor := 0; cursor < len(pending); cursor++ {
		node := pending[cursor]
		// Passengers is structural state, reconstructed from live children on save.
		// Keeping the input compound in ExtraNBT would resurrect a removed last rider.
		delete(node.parent.ExtraNBT, "Passengers")
		list, ok := node.save["Passengers"].(nbt.List)
		if !ok {
			continue
		}
		for _, value := range list.Items {
			child, ok := value.(map[string]any)
			if !ok {
				continue
			}
			name, _ := child["id"].(string)
			name = strings.TrimPrefix(name, "minecraft:")
			if !nativePassengerTreeName(name) {
				continue
			}
			pos, ok := nbtVec3(child["Pos"])
			if !ok {
				pos = [3]float64{node.parent.X, node.parent.Y, node.parent.Z}
			}
			var e *Entity
			if ordinaryVehicle(name) {
				id, registered := EntityTypeID(name)
				if !registered {
					continue
				}
				e = s.entities.Spawn(&Entity{Type: id, Dim: d.name, X: pos[0], Y: pos[1], Z: pos[2]})
			} else {
				e, ok = s.summonMobRaw(d, name, pos[0], pos[1], pos[2], false, false)
				if !ok {
					continue
				}
			}
			if u, ok := child["UUID"].([]int32); ok && len(u) == 4 {
				e.UUID = uuidFromInts(u)
			}
			s.applyEntitySave(e, child)
			e.CartYawO = e.Yaw
			e.dataSync = nil
			e.MetaDirty = false
			e.Riding = node.parent.ID
			node.parent.Passengers = append(node.parent.Passengers, e.ID)
			pending = append(pending, treeNode{e, child})
		}
	}
}
