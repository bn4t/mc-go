package server

type playerEffectAttributeTemplate struct {
	attribute string
	id        string
	amount    float64
	operation int
}

func (s *Server) setPlayerEffectAttributes(sess *Session, id int32, inst mobEffectInst) {
	if s.entities == nil {
		return
	}
	name, ok := MobEffectName(id)
	if !ok {
		return
	}
	e, ok := s.entities.GetPtr(sess.entityID.Load())
	if !ok {
		return
	}
	for _, template := range playerEffectAttributeTemplates[name] {
		if e.AttrMods == nil {
			e.AttrMods = map[string]map[string]attrMod{}
		}
		if e.AttrMods[template.attribute] == nil {
			e.AttrMods[template.attribute] = map[string]attrMod{}
		}
		e.AttrMods[template.attribute][template.id] = attrMod{value: template.amount * float64(inst.amplifier+1), op: template.operation, permanent: true}
		e.markAttrDirty(template.attribute)
	}
}

// Only an actual effect transition removes these IDs. A command modifier with
// the same identifier and no associated active effect remains ordinary state.
func (s *Server) removePlayerEffectAttributes(sess *Session, id int32) {
	if s.entities == nil {
		return
	}
	name, ok := MobEffectName(id)
	if !ok {
		return
	}
	e, ok := s.entities.GetPtr(sess.entityID.Load())
	if !ok {
		return
	}
	for _, template := range playerEffectAttributeTemplates[name] {
		if _, present := e.AttrMods[template.attribute][template.id]; present {
			delete(e.AttrMods[template.attribute], template.id)
			e.markAttrDirty(template.attribute)
		}
	}
}
