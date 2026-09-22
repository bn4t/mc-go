package server

import (
	"bytes"
	"errors"

	"mc-go/internal/nbt"
)

// errMixedArguments reports translation arguments that cannot share one
// NBT list.
var errMixedArguments = errors.New("server: mixed translation arguments")

// component is a chat component in the shape vanilla's built-in
// commands produce: a plain literal, or a translation key with typed
// arguments. Vanilla words its command replies as translatable
// components so the client renders them in its own language, and each
// argument keeps its own NBT type - a game time is a long, a timeline
// name is a string, a timeline value is a compound - so the wire form
// is reproduced field for field rather than flattened to text.
type component struct {
	// text is a literal component's text. A component with a translate
	// key carries no text of its own.
	text string
	// translate is the translation key, e.g.
	// "commands.time.query.gametime".
	translate string
	// with holds the translation arguments: string, int32, int64,
	// float32, float64, a nestedCompound, or a whole component (an
	// /item reply passes the target name and the item brackets as
	// compounds among numbers).
	with []any
	// color styles the component, "red" for command errors.
	color string
	// extra holds sibling components; vanilla puts a command's message
	// in extra when it also carries a style.
	extra []component
	// emptyKey is a literal rendered under the empty entry name vanilla
	// uses inside command context messages, {"": "tick "}. Such a
	// component carries no text field of its own. forceCompound keeps
	// an empty emptyKey a compound — /data's pretty SNBT prints {"": ""}
	// spacers that would otherwise collapse to a bare string inside a
	// compound-typed extra list.
	emptyKey      string
	forceCompound bool
	// underlined and italic are the style flags vanilla sets on the
	// token a failed parse points at, and on the cursor marker.
	underlined bool
	italic     bool
	// click is the input of a suggest_command click event, which is how
	// vanilla makes a failed command clickable in the client.
	click string
	// copyTo carries a copy_to_clipboard click event — /seed's bracketed
	// seed copies itself on click.
	copyTo string
	// runCmd carries a run_command click event — /fetchprofile's bracketed
	// give-item and summon actions run themselves on click.
	runCmd string
	// insertion is the shift-click insertion text, which vanilla sets on
	// the entity names its replies carry (/item names its target).
	insertion string
	// hover is a hover_event compound, e.g. show_entity on an /item
	// target or show_item on the item its brackets name.
	hover map[string]any
}

// nestedCompound is an NBT compound used as a translation argument.
// Vanilla passes resource keys and numbers this way: /time set reports
// {"" -> "minecraft:overworld"} and {"" -> 5000}, an entry with an
// empty name.
type nestedCompound struct {
	key   string
	value any
}

// literal builds a plain-text component.
func literal(text string) component { return component{text: text} }

// translatable builds a translation component, vanilla's
// Component.translatable.
func translatable(key string, with ...any) component {
	return component{translate: key, with: with}
}

// commandError builds the component vanilla replies with when a command
// fails: an empty red literal carrying the message as its extra.
func commandError(key string, with ...any) component {
	return component{
		color: "red",
		extra: []component{translatable(key, with...)},
		text:  "",
	}
}

// isPlain reports whether the component is a bare literal, which
// vanilla sends as a string rather than a compound.
func (c component) isPlain() bool {
	return c.translate == "" && c.color == "" && len(c.extra) == 0 &&
		c.emptyKey == "" && !c.forceCompound && c.click == "" && c.copyTo == "" &&
		c.runCmd == "" &&
		c.insertion == "" && len(c.hover) == 0 && !c.underlined && !c.italic
}

// nested renders the component as an NBT compound.
func (c component) nested() map[string]any {
	m := map[string]any{}
	if c.click != "" {
		m["click_event"] = map[string]any{
			"action":  "suggest_command",
			"command": c.click,
		}
	}
	if c.copyTo != "" {
		m["click_event"] = map[string]any{
			"action": "copy_to_clipboard",
			"value":  c.copyTo,
		}
	}
	if c.runCmd != "" {
		m["click_event"] = map[string]any{
			"action":  "run_command",
			"command": c.runCmd,
		}
	}
	if c.color != "" {
		m["color"] = c.color
	}
	if len(c.extra) > 0 {
		m["extra"] = componentsNBT(c.extra)
	}
	if c.italic {
		m["italic"] = int8(1)
	}
	if c.insertion != "" {
		m["insertion"] = c.insertion
	}
	if len(c.hover) > 0 {
		m["hover_event"] = c.hover
	}
	if c.translate != "" {
		m["translate"] = c.translate
	}
	// The three content forms are exclusive: a translation carries no
	// text field at all - not even an empty one, which the real server
	// leaves out - an empty-keyed literal puts its text under "", and
	// anything else is a plain literal with its text spelled out.
	switch {
	case c.translate != "":
	case c.emptyKey != "" || c.forceCompound:
		m[""] = c.emptyKey
	default:
		m["text"] = c.text
	}
	if c.underlined {
		m["underlined"] = int8(1)
	}
	if len(c.with) > 0 {
		l, err := argumentList(c.with)
		if err == nil {
			m["with"] = l
		}
	}
	return m
}

// errorContext is the second message vanilla sends after a failed
// parse. It repeats the input as a gray, clickable component with the
// token the parse stopped on marked in red, and ends at the cursor the
// real server reports: the start of the token it failed on, or the end
// of the last one it consumed.
//
// The shape was read off the real server byte for byte, including the
// parts that look arbitrary: the input is echoed in ten-character
// windows around the cursor with "..." when it was clipped, empty
// pieces are left out rather than sent blank, and the click event
// suggests the whole input as "/<input>".
func errorContext(input string, cursor int) component {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	var items []component
	if cursor > 10 {
		items = append(items, component{emptyKey: "..."})
	}
	start := cursor - 10
	if start < 0 {
		start = 0
	}
	if prefix := input[start:cursor]; prefix != "" {
		items = append(items, component{emptyKey: prefix})
	}
	if tail := input[cursor:]; tail != "" {
		items = append(items, component{color: "red", underlined: true, text: tail})
	}
	items = append(items, component{color: "red", italic: true, translate: "command.context.here"})
	return component{
		color: "red",
		extra: []component{{
			click: "/" + input,
			color: "gray",
			extra: items,
			text:  "",
		}},
		text: "",
	}
}

// menuTitle encodes an Open Screen title the way vanilla 26.2 does: a
// compound holding only the menu's translation key, e.g.
// {"translate": "container.chest"} for an unnamed chest. Measured
// across every menu (see TestContainerOpenParity): the real server
// never sends the literal English name for an unrenamed container.
func menuTitle(key string) []byte {
	return component{translate: key}.content()
}

// content encodes the component as the payload of a System Chat packet.
// A plain literal stays a TAG_String root, the compact form vanilla
// also uses for unstyled text.
func (c component) content() []byte {
	if c.isPlain() {
		return textComponentContent(c.text)
	}
	var buf bytes.Buffer
	if err := nbt.WriteUnnamed(&buf, c.nested()); err != nil {
		return textComponentContent(c.text)
	}
	return buf.Bytes()
}

// componentsNBT renders a list of components as its NBT list tag.
// Vanilla decides the element type per list: an all-literal list is a
// String list, but a compound anywhere makes every element a compound —
// a bare literal then takes its empty-key form {"": text}.
func componentsNBT(cs []component) nbt.List {
	// An unstyled empty-key piece is a literal like a plain one: a list
	// of only literals is a String list ({"extra":["{","}"]}), and in a
	// compound list each renders {"": text}.
	stringable := func(c component) bool {
		if c.isPlain() {
			return true
		}
		return c.emptyKey != "" && !c.forceCompound && c.color == "" &&
			len(c.extra) == 0 && c.click == "" && c.insertion == "" &&
			len(c.hover) == 0 && c.translate == "" && !c.underlined && !c.italic
	}
	compound := false
	for _, c := range cs {
		if !stringable(c) {
			compound = true
			break
		}
	}
	items := make([]any, 0, len(cs))
	for _, c := range cs {
		if stringable(c) {
			text := c.text
			if c.emptyKey != "" {
				text = c.emptyKey
			}
			if compound {
				items = append(items, map[string]any{"": text})
			} else {
				items = append(items, text)
			}
		} else {
			items = append(items, c.nested())
		}
	}
	elem := byte(nbt.TagCompound)
	if !compound {
		elem = nbt.TagString
	}
	return nbt.List{Elem: elem, Items: items}
}

// argumentList builds the "with" list, whose element type comes from the
// arguments themselves: vanilla sends a list of longs for a game time
// and a list of compounds for a timeline, and the client's decoder
// rejects a list that mixes tags.
func argumentList(args []any) (nbt.List, error) {
	compound := false
	for _, a := range args {
		switch a.(type) {
		case nestedCompound, component:
			compound = true
		}
	}
	items := make([]any, 0, len(args))
	elem := byte(0)
	for _, a := range args {
		v, tag := argumentValue(a)
		// NBT lists are homogeneous: once an argument is a compound
		// every primitive arg takes its empty-key form {"": x}, the
		// same rule componentsNBT applies to mixed lists.
		if compound && tag != nbt.TagCompound {
			v, tag = map[string]any{"": v}, nbt.TagCompound
		}
		if elem == 0 {
			elem = tag
		}
		if tag != elem {
			return nbt.List{}, errMixedArguments
		}
		items = append(items, v)
	}
	return nbt.List{Elem: elem, Items: items}, nil
}

// argumentValue converts one translation argument to its NBT value and
// tag.
func argumentValue(a any) (any, byte) {
	switch v := a.(type) {
	case string:
		return v, nbt.TagString
	case int:
		return int32(v), nbt.TagInt
	case int32:
		return v, nbt.TagInt
	case int64:
		return v, nbt.TagLong
	case float32:
		return v, nbt.TagFloat
	case float64:
		return v, nbt.TagDouble
	case nestedCompound:
		value, _ := argumentValue(v.value)
		return map[string]any{v.key: value}, nbt.TagCompound
	case component:
		return v.nested(), nbt.TagCompound
	case map[string]any:
		return v, nbt.TagCompound
	}
	return "", nbt.TagString
}
