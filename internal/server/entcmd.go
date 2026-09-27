package server

// Entity-manipulation commands measured against the vanilla 26.2
// server jar:
//
//	rotate <target> <yaw> <pitch>                player_rotation packet
//	rotate <target> facing <x> <y> <z>           player_look_at packet
//	rotate <target> facing entity <e> [anchor]   look_at's entity form
//	ride <target> mount|dismount [<vehicle>]     set_passengers + a
//	                                             rider position sync
//	spreadplayers <x> <z> <sep> <range>
//	  [under <maxHeight>] <respectTeams> <ts>    random teleports; the
//	                                             reply's average distance
//	                                             is over team positions
//	damage <target> <amount> [<type>
//	  [at <pos> | by <e> [from <e>]]]            damage_event + health
//	                                             sync, death broadcast

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"

	"mc-go/internal/protocol"
)

func (s *Server) registerEntCommands() {
	for _, name := range []string{"rotate", "ride", "spreadplayers", "damage", "test"} {
		n := name
		s.registerComponentCommand(n, func(player string, args []string) reply {
			switch n {
			case "rotate":
				return s.runRotate(player, args)
			case "ride":
				return s.runRide(player, args)
			case "spreadplayers":
				return s.runSpreadPlayers(player, args)
			case "test":
				return s.runTest(player, args)
			}
			return s.runDamage(player, args)
		})
	}
}

// resolveSingleEntity parses an entity argument that must match exactly
// one entity: a bare name that resolves to nobody fails
// notfound.entity (the argument is an entity one, not a player one), a
// selector matching more than one fails entity.toomany with the cursor
// back at the input start — vanilla underlines the whole command.
func (s *Server) resolveSingleEntity(sender *Session, tok string, tokIdx int) (*dataEntity, *reply) {
	if len(tok) == 0 || (tok[0] != '@' && s.findSessionByName(tok) == nil) {
		r := componentReply(commandError("argument.entity.notfound.entity"))
		return nil, &r
	}
	targets, rep := s.selectDataEntities(sender, tok, tokIdx, true)
	if rep != nil {
		return nil, rep
	}
	if len(targets) == 0 {
		r := componentReply(commandError("argument.entity.notfound.entity"))
		return nil, &r
	}
	return targets[0], nil
}

// entityIDsOf is the wire identity of a dataEntity for the entity-id
// fields of the passengers and look_at packets.
func entityIDOf(d *dataEntity) int32 {
	if d.sess != nil {
		return d.sess.entityID.Load()
	}
	return d.mob.ID
}

// entityPtr is the shared entity record a dataEntity resolves to —
// passengers and vehicles live on it.
func (s *Server) entityPtr(d *dataEntity) *Entity {
	id := entityIDOf(d)
	if p, ok := s.entities.GetPtr(id); ok {
		return p
	}
	return nil
}

// ---- rotate ----

func (s *Server) runRotate(player string, args []string) reply {
	sender := s.findSessionByName(player)
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	target, rep := s.resolveSingleEntity(sender, args[0], 1)
	if rep != nil {
		return *rep
	}
	if args[1] == "facing" {
		return s.runRotateFacing(sender, target, args[2:])
	}
	yaw, pitch, rr := s.parseRotation(sender, args[1:], 2)
	if rr != nil {
		return *rr
	}
	s.rotateTo(target, yaw, pitch)
	return opsEchoReply(translatable("commands.rotate.success", target.nameComp()))
}

// rotateTo applies an absolute look direction: a player gets the
// player_rotation packet and the stored angles; a mob turns via the
// next sync pass.
func (s *Server) rotateTo(target *dataEntity, yaw, pitch float32) {
	if target.sess != nil {
		x, y, z, _, _, onG := target.sess.Pos()
		target.sess.setPos(x, y, z, yaw, pitch, onG)
		var p protocol.Buffer
		p.Float(yaw)
		p.Byte(0)
		p.Float(pitch)
		p.Byte(0)
		_ = target.sess.write(target.sess.ids().PlayPlayerRotation, p.Bytes())
		return
	}
	target.mob.Yaw, target.mob.Pitch = yaw, pitch
	target.mob.teleportDelay = 401
}

// runRotateFacing handles "facing <x> <y> <z>" and
// "facing entity <e> [eyes|feet]".
func (s *Server) runRotateFacing(sender *Session, target *dataEntity, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 3)
	}
	if args[0] == "entity" {
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 4)
		}
		if len(args) > 3 {
			return errToken("command.unknown.argument", 6)
		}
		anchor := byte(0)
		if len(args) == 3 {
			switch args[2] {
			case "eyes":
				anchor = 1
			case "feet":
			default:
				return errToken("argument.anchor.invalid", 5, args[2])
			}
		}
		src, rep := s.resolveSingleEntity(sender, args[1], 4)
		if rep != nil {
			return *rep
		}
		x, y, z, _, _ := src.posRot()
		if anchor == 1 {
			y += eyeHeightOf(src)
		}
		s.sendLookAtEntity(target, x, y, z, entityIDOf(src), anchor)
		return opsEchoReply(translatable("commands.rotate.success", target.nameComp()))
	}
	if len(args) > 3 {
		return errToken("command.unknown.argument", 6)
	}
	bx, by, bz := float64(0), float64(0), float64(0)
	if sender != nil {
		bx, by, bz, _, _, _ = sender.Pos()
	}
	coords := [3]float64{}
	// The vector reads greedily: a token that will not parse fails
	// parsing.double.expected, while running out mid-vector fails
	// pos3d.incomplete at the vector's first token.
	for i := 0; i < len(args); i++ {
		base := []float64{bx, by, bz}[i]
		v, err := parseDouble(args[i], base, i != 1)
		if err != nil {
			return errToken("parsing.double.expected", 3+i)
		}
		coords[i] = v
	}
	if len(args) < 3 {
		return errToken("argument.pos3d.incomplete", 3)
	}
	s.sendLookAt(target, coords[0], coords[1], coords[2])
	return opsEchoReply(translatable("commands.rotate.success", target.nameComp()))
}

// eyeHeightOf approximates the eyes anchor: vanilla multiplies each
// entity's height by its per-type eye scale; 0.85 of a typical body
// height is close enough for the look_at packet.
func eyeHeightOf(d *dataEntity) float64 {
	if d.sess != nil {
		return 1.62
	}
	return 0.85 * 0.9
}

// sendLookAt emits player_look_at aiming at a position — packet shape
// [aim byte][x y z doubles]; a player target is the only one the
// packet exists for.
func (s *Server) sendLookAt(target *dataEntity, x, y, z float64) {
	if target.sess == nil {
		return
	}
	var p protocol.Buffer
	p.Byte(0)
	p.Double(x)
	p.Double(y)
	p.Double(z)
	p.Byte(0) // no entity follows
	_ = target.sess.write(target.sess.ids().PlayPlayerLookAt, p.Bytes())
}

// sendLookAtEntity is sendLookAt's entity form: the position doubles
// precede an entity marker, the entity id, and the anchor the aim used.
func (s *Server) sendLookAtEntity(target *dataEntity, x, y, z float64, id int32, anchor byte) {
	if target.sess == nil {
		return
	}
	var p protocol.Buffer
	p.Byte(0)
	p.Double(x)
	p.Double(y)
	p.Double(z)
	p.Byte(1)
	p.VarInt(id)
	p.VarInt(int32(anchor))
	_ = target.sess.write(target.sess.ids().PlayPlayerLookAt, p.Bytes())
}

// ---- ride ----

func (s *Server) runRide(player string, args []string) reply {
	sender := s.findSessionByName(player)
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	rider, rep := s.resolveSingleEntity(sender, args[0], 1)
	if rep != nil {
		return *rep
	}
	switch args[1] {
	case "dismount":
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		return s.runRideDismount(rider)
	case "mount":
		if len(args) < 3 {
			return errTokenEnd("command.unknown.command", len(args)+1)
		}
		if len(args) > 3 {
			return errToken("command.unknown.argument", 4)
		}
		return s.runRideMount(sender, rider, args[2])
	default:
		return errToken("command.unknown.argument", 2)
	}
}

// vehicleOf finds the entity whose passenger list holds id.
func (s *Server) vehicleOf(id int32) *Entity {
	for _, e := range s.entities.All() {
		for _, p := range e.Passengers {
			if p == id {
				ptr, _ := s.entities.GetPtr(e.ID)
				return ptr
			}
		}
	}
	return nil
}

func (s *Server) runRideDismount(rider *dataEntity) reply {
	id := entityIDOf(rider)
	veh := s.vehicleOf(id)
	if veh == nil {
		return componentReply(commandError("commands.ride.not_riding", rider.nameComp()))
	}
	keep := veh.Passengers[:0]
	for _, p := range veh.Passengers {
		if p != id {
			keep = append(keep, p)
		}
	}
	veh.Passengers = append([]int32(nil), keep...)
	s.broadcastSetPassengers(veh)
	// The rider's client snaps back to a real position once it stops
	// riding; vanilla teleports it to the vehicle's spot.
	if rider.sess != nil {
		rider.sess.riding.Store(0)
		x, y, z, yaw, pitch, _ := rider.sess.Pos()
		x, y, z = veh.X, veh.Y, veh.Z
		s.teleportPlayerRot(rider.sess.getName(), x, y, z, yaw, pitch)
	}
	return opsEchoReply(translatable("commands.ride.dismount.success",
		rider.nameComp(), entityDisplayName(veh)))
}

func (s *Server) runRideMount(sender *Session, rider *dataEntity, vehTok string) reply {
	veh, rep := s.resolveSingleEntity(sender, vehTok, 3)
	if rep != nil {
		return *rep
	}
	rid, vid := entityIDOf(rider), entityIDOf(veh)
	if cur := s.vehicleOf(rid); cur != nil {
		return componentReply(commandError("commands.ride.already_riding",
			rider.nameComp(), entityDisplayName(cur)))
	}
	if veh.sess != nil {
		return componentReply(commandError("commands.ride.mount.failure.cant_ride_players"))
	}
	if rid == vid {
		return componentReply(commandError("commands.ride.mount.failure.loop"))
	}
	vehEnt := s.entityPtr(veh)
	if vehEnt == nil {
		return componentReply(commandError("argument.entity.notfound.entity"))
	}
	vehEnt.Passengers = append(append([]int32(nil), vehEnt.Passengers...), rid)
	// The rider's client teleports onto the seat before the passenger
	// list lands — vanilla's packet order is position then
	// set_passengers. A command mount starts riding like an interact
	// mount: rideTickStep seat-follows and banks the vehicle stat.
	if rider.sess != nil {
		rider.sess.riding.Store(vid)
		vehEnt.VehAnchor = [3]float64{vehEnt.X, vehEnt.Y, vehEnt.Z}
		_, _, _, yaw, pitch, _ := rider.sess.Pos()
		// Seat at mount like positionRider does — rideTickStep measures
		// ride stats from the seat, not the pre-mount position.
		s.teleportPlayerRot(rider.sess.getName(), vehEnt.X, vehEnt.Y+passengerSeatY(vehEnt)-0.6, vehEnt.Z, yaw, pitch)
	}
	s.broadcastSetPassengers(vehEnt)
	return opsEchoReply(translatable("commands.ride.mount.success",
		rider.nameComp(), veh.nameComp()))
}

// ---- spreadplayers ----

func (s *Server) runSpreadPlayers(player string, args []string) reply {
	sender := s.findSessionByName(player)
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 1)
	}
	if len(args) == 1 {
		return errToken("argument.pos2d.incomplete", 1)
	}
	// Optional "under <maxHeight>" tail keeps the same tail shape.
	rest := args
	maxHeight := 0
	if len(rest) > 5 && rest[4] == "under" {
		if len(rest) < 8 {
			return errTokenEnd("command.unknown.command", len(rest)+1)
		}
		h, err := strconv.Atoi(rest[5])
		if err != nil {
			return errToken("parsing.int.expected", 6)
		}
		maxHeight = h
		rest = append(rest[:4], rest[6:]...)
	}
	if len(rest) < 6 {
		return errTokenEnd("command.unknown.command", len(rest)+1)
	}
	bx, _, bz := float64(0), float64(0), float64(0)
	if sender != nil {
		bx, _, bz, _, _, _ = sender.Pos()
	}
	cx, err := parseDouble(rest[0], bx, true)
	if err != nil {
		return errToken("parsing.double.expected", 1)
	}
	cz, err := parseDouble(rest[1], bz, true)
	if err != nil {
		return errToken("parsing.double.expected", 2)
	}
	sep, err := floatArg(rest[2])
	if err != nil {
		return errToken("parsing.float.expected", 3)
	}
	if sep < 0 {
		return errToken("argument.float.low", 3, float32(0), sep)
	}
	rng, err := floatArg(rest[3])
	if err != nil {
		return errToken("parsing.float.expected", 4)
	}
	if rng < sep {
		return errToken("argument.float.low", 4, sep, rng)
	}
	respectTeams, err := strconv.ParseBool(rest[4])
	if err != nil {
		return errToken("parsing.bool.invalid", 5, rest[4])
	}
	if len(rest) > 6 {
		return errToken("command.unknown.argument", 7)
	}
	tok := rest[5]
	if len(tok) == 0 || (tok[0] != '@' && s.findSessionByName(tok) == nil) {
		return componentReply(commandError("argument.entity.notfound.entity"))
	}
	targets, rep := s.selectDataEntities(sender, tok, 6, false)
	if rep != nil {
		return *rep
	}
	if len(targets) == 0 {
		return componentReply(commandError("argument.entity.notfound.entity"))
	}
	_ = maxHeight // maxHeight only bounds the spawn-height scan.
	return s.spreadTargets(sender, targets, cx, cz, float64(sep), float64(rng), respectTeams)
}

// spreadTargets teleports each target to a random spot in the
// center±range square and replies with the spread statistics vanilla
// reports: the slot count (teams share slots), the center, and the
// average nearest-neighbour distance between the assigned positions —
// one shared slot leaves every entity's minimum at MaxDouble, which
// averages to +Inf and formats as "Infinity".
func (s *Server) spreadTargets(sender *Session, targets []*dataEntity, cx, cz, sep, rng float64, respectTeams bool) reply {
	slots := len(targets)
	teamPos := map[*dataEntity]int{}
	if respectTeams {
		// Entities group by team; this server has no teams, so every
		// entity shares one slot — one position for the whole group.
		slots = 1
		for _, t := range targets {
			teamPos[t] = 0
		}
	}
	type spreadPos struct{ x, z float64 }
	positions := make([]spreadPos, slots)
	for i := range positions {
		positions[i] = spreadPos{
			x: cx + (rand.Float64()*2-1)*rng,
			z: cz + (rand.Float64()*2-1)*rng,
		}
	}
	dimName := dimOverworld
	if sender != nil {
		dimName = sender.dimName()
	}
	dim := s.dimensionByName(dimName)
	for i, t := range targets {
		slot := i
		if respectTeams {
			slot = teamPos[t]
		}
		p := positions[slot]
		x, z := math.Floor(p.x)+0.5, math.Floor(p.z)+0.5
		y := float64(s.surfaceY(dim, int(math.Floor(x)), int(math.Floor(z)))) + 1
		if t.sess != nil {
			_, _, _, yaw, pitch, _ := t.sess.Pos()
			s.teleportPlayerRot(t.sess.getName(), x, y, z, yaw, pitch)
		} else {
			t.mob.X, t.mob.Y, t.mob.Z = x, y, z
			t.mob.teleportDelay = 401
			s.broadcastEntityTeleport(nil, t.mob.ID, x, y, z, t.mob.Yaw, t.mob.Pitch, t.mob.Grounded)
		}
	}
	dist := 0.0
	if len(targets) >= 2 {
		sum := math.MaxFloat64 * float64(len(targets))
		if slots > 1 {
			sum = 0
			for i := range targets {
				slot := i
				if respectTeams {
					slot = teamPos[targets[i]]
				}
				p := positions[slot]
				min := math.MaxFloat64
				for j, q := range positions {
					if j == slot {
						continue
					}
					d := math.Hypot(p.x-q.x, p.z-q.z)
					if d < min {
						min = d
					}
				}
				sum += min
			}
		}
		dist = sum / float64(len(targets))
	}
	key := "commands.spreadplayers.success.entities"
	if respectTeams {
		key = "commands.spreadplayers.success.teams"
	}
	var distStr string
	if math.IsInf(dist, 0) {
		distStr = "Infinity"
	} else {
		distStr = fmt.Sprintf("%.2f", dist)
	}
	return opsEchoReply(translatable(key,
		nestedCompound{key: "", value: int32(slots)},
		nestedCompound{key: "", value: float32(cx)},
		nestedCompound{key: "", value: float32(cz)},
		nestedCompound{key: "", value: distStr}))
}

// ---- damage ----

// damageTypes lists the vanilla 26.2 damage_type registry and each
// entry's death-message key suffix (its message_id).
var damageTypes = map[string]string{
	"arrow": "arrow", "bad_respawn_point": "badRespawnPoint",
	"cactus": "cactus", "campfire": "inFire", "cramming": "cramming",
	"dragon_breath": "dragonBreath", "drown": "drown", "dry_out": "dryout",
	"ender_pearl": "fall", "explosion": "explosion", "fall": "fall",
	"falling_anvil": "anvil", "falling_block": "fallingBlock",
	"falling_stalactite": "fallingStalactite", "fireball": "fireball",
	"fireworks": "fireworks", "fly_into_wall": "flyIntoWall",
	"freeze": "freeze", "generic": "generic", "generic_kill": "genericKill",
	"hot_floor": "hotFloor", "in_fire": "inFire", "in_wall": "inWall",
	"indirect_magic": "indirectMagic", "lava": "lava",
	"lightning_bolt": "lightningBolt", "mace_smash": "maceSmash",
	"magic": "magic", "mob_attack": "mob", "mob_attack_no_aggro": "mob",
	"mob_projectile": "mob", "on_fire": "onFire", "out_of_world": "outOfWorld",
	"outside_border": "outsideBorder", "player_attack": "player",
	"player_explosion": "explosion.player", "sonic_boom": "sonicBoom",
	"spear": "spear", "spit": "mob", "stalagmite": "stalagmite",
	"starve": "starve", "sting": "sting", "sulfur_cube_hot": "sulfurCubeHot",
	"sweet_berry_bush": "sweetBerryBush", "thorns": "thorns",
	"thrown": "thrown", "trident": "trident",
	"unattributed_fireball": "onFire", "wind_charge": "mob",
	"wither": "wither", "wither_skull": "witherSkull",
}

func (s *Server) runDamage(player string, args []string) reply {
	sender := s.findSessionByName(player)
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", len(args)+1)
	}
	target, rep := s.resolveSingleEntity(sender, args[0], 1)
	if rep != nil {
		return *rep
	}
	amount, err := floatArg(args[1])
	if err != nil {
		return errToken("parsing.float.expected", 2)
	}
	if amount < 0 {
		return errToken("argument.float.low", 2, float32(0), amount)
	}
	dmgType := "generic"
	var causeEnt, directEnt *dataEntity
	var atPos [3]float64
	hasAt := false
	if len(args) > 2 {
		t := strings.TrimPrefix(args[2], "minecraft:")
		if _, ok := damageTypes[t]; !ok {
			return errTokenEnd("argument.resource.not_found", 3, args[2], "minecraft:damage_type")
		}
		dmgType = t
	}
	if len(args) > 3 {
		switch args[3] {
		case "at":
			if len(args) != 7 {
				if len(args) < 7 {
					return errToken("argument.pos3d.incomplete", 5)
				}
				return errToken("command.unknown.argument", 8)
			}
			bx, by, bz := float64(0), float64(0), float64(0)
			if sender != nil {
				bx, by, bz, _, _, _ = sender.Pos()
			}
			for i := 0; i < 3; i++ {
				base := []float64{bx, by, bz}[i]
				v, err := parseDouble(args[4+i], base, i != 1)
				if err != nil {
					return errToken("parsing.double.expected", 5+i)
				}
				atPos[i] = v
			}
			hasAt = true
		case "by":
			if len(args) < 5 {
				return errTokenEnd("command.unknown.command", len(args)+1)
			}
			var rep *reply
			directEnt, rep = s.resolveSingleEntity(sender, args[4], 5)
			if rep != nil {
				return *rep
			}
			if len(args) > 5 {
				if len(args) != 7 || args[5] != "from" {
					return errToken("command.unknown.argument", 6)
				}
				causeEnt, rep = s.resolveSingleEntity(sender, args[6], 7)
				if rep != nil {
					return *rep
				}
			}
		default:
			return errToken("command.unknown.argument", 4)
		}
	}
	return s.applyDamageCommand(sender, target, amount, dmgType, directEnt, causeEnt, hasAt, atPos)
}

// applyDamageCommand deals the hit and answers success/invulnerable.
// Vanilla checks invulnerability after all parsing, so every argument
// error above wins over it.
func (s *Server) applyDamageCommand(sender *Session, target *dataEntity, amount float32, dmgType string, direct, cause *dataEntity, hasAt bool, atPos [3]float64) reply {
	var srcID, causeID int32
	if direct != nil {
		srcID = entityIDOf(direct)
	}
	if cause != nil {
		causeID = entityIDOf(cause)
	} else if direct != nil {
		causeID = srcID
	}
	if target.sess != nil {
		sess := target.sess
		if sess.dead || sessInvulnerableTo(sess, dmgType) {
			return componentReply(commandError("commands.damage.invulnerable"))
		}
		var msg component
		if attacker := cause; attacker != nil || direct != nil {
			if attacker == nil {
				attacker = direct
			}
			msg = translatable("death.attack."+damageTypes[dmgType], target.nameComp(), attacker.nameComp())
		} else {
			msg = translatable("death.attack."+damageTypes[dmgType], target.nameComp())
		}
		s.damageWith(sess, amount, msg, damageSource{typeName: dmgType, cause: causeID, direct: srcID,
			hasPos: hasAt, x: atPos[0], y: atPos[1], z: atPos[2]})
		return opsEchoReply(translatable("commands.damage.success",
			nestedCompound{key: "", value: amount}, target.nameComp()))
	}
	mob := target.mob
	// Vanilla's DamageSource falls back to the direct entity when no
	// `from` cause is given — `by @s` provokes just like a punch.
	var killer *Session
	if cause != nil {
		killer = cause.sess
	} else if direct != nil {
		killer = direct.sess
	}
	// dealDefaultKnockback runs inside damageMob for the landed hit.
	_, _, died := s.damageMob(mob, amount, dmgType, killer,
		&damageInfo{typeName: dmgType, cause: causeID, direct: srcID,
			hasPos: hasAt, x: atPos[0], y: atPos[1], z: atPos[2]})
	_ = died
	return opsEchoReply(translatable("commands.damage.success",
		nestedCompound{key: "", value: amount}, target.nameComp()))
}

// ---- test (GameTest) ----

// runTest implements /test's literal surface. There is no test
// instance framework, so executing subcommands land on their
// "no tests" replies, matching vanilla on an empty world.
func (s *Server) runTest(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	sender := s.findSessionByName(player)
	// radiusTail parses the optional [radius] int every
	// run/clear/reset literal takes.
	radiusTail := func(done reply) reply {
		if len(args) == 1 {
			return done
		}
		if _, err := strconv.Atoi(args[1]); err != nil {
			return errToken("parsing.int.expected", 2)
		}
		if len(args) == 2 {
			return done
		}
		return errToken("command.unknown.argument", 3)
	}
	switch args[0] {
	case "runclosest", "runthese", "runfailed", "runthat":
		return radiusTail(componentReply(translatable("commands.test.no_tests")))
	case "clearall", "clearthese", "clearthat",
		"resetclosest", "resetthat", "resetthese":
		return radiusTail(componentReply(commandError("commands.test.clear.error.no_tests")))
	case "pos":
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		x, y, z := int32(0), int32(0), int32(0)
		if sender != nil {
			fx, fy, fz, _, _, _ := sender.Pos()
			x = int32(math.Floor(fx))
			y = int32(math.Floor(fy))
			z = int32(math.Floor(fz))
		}
		return componentReply(commandError("commands.test.error.no_test_containing_pos", x, y, z))
	case "stop":
		if len(args) != 1 {
			return errTokenEnd("command.unknown.command", 1)
		}
		return reply{}
	case "run", "runmultiple", "locate", "verify":
		if len(args) == 1 {
			return errTokenEnd("command.unknown.command", 1)
		}
		return errTokenEnd("argument.resource_selector.not_found", len(args)+1,
			testResourceID(args[1]), "minecraft:test_instance")
	case "create":
		if len(args) == 1 {
			return errTokenEnd("command.unknown.command", 1)
		}
		ok := len(args) == 2 || len(args) == 3 || len(args) == 5
		for i := 2; ok && i < len(args); i++ {
			if _, err := strconv.Atoi(args[i]); err != nil {
				return errToken("parsing.int.expected", i+1)
			}
		}
		if !ok {
			return errTokenEnd("command.unknown.command", len(args))
		}
		return opsEchoReply(translatable("commands.test.create.success",
			testResourceID(args[1])))
	default:
		return errToken("command.unknown.argument", 1)
	}
}

// testResourceID renders a test-instance name the way the error
// messages do: bare names get the minecraft namespace.
func testResourceID(name string) string {
	if !strings.Contains(name, ":") {
		return "minecraft:" + name
	}
	return name
}
