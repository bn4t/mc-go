package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// The ender chest: a 27-slot generic_9x3 menu over the player's own ender
// storage, not over the block. Breaking the block drops nothing it held
// (there is no per-position storage), and two chests in two places show
// the same items.

// isEnderChestBlock reports whether a block state is an ender chest.
func isEnderChestBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if !ok {
		return false
	}
	return name == "ender_chest"
}

// openEnderChest shows the player's ender storage and its contents. The
// clicks run through the chest path: the menu has the chest's shape
// (27 over 36) and none of the other menus' restrictions.
func (s *Server) openEnderChest(sess *Session, pos world.Pos) {
	id := sess.server.nextWindow.Add(1)
	sess.windowMu.Lock()
	sess.window = &openWindow{id: id, pos: pos, dim: sess.dimension(), ender: &sess.ender, kind: menuGeneric9x3}
	sess.windowState = 0
	s.emitContainerOpen(sess, pos)
	sess.windowMu.Unlock()

	var b protocol.Buffer
	b.VarInt(id)
	b.VarInt(menuGeneric9x3)
	b.Raw(menuTitle("container.enderchest"))
	_ = sess.write(sess.ids().PlayOpenScreen, b.Bytes())
	s.sendChestContent(sess)
}
