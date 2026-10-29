package server

import "sort"

// Advancement tabs use a tidy tree drawing: displayed depth determines X,
// parents center over their outer children, and adjacent contours stay one
// unit apart. This independent implementation uses the standard linear tidy
// tree algorithm (Buchheim, Jünger and Leipert, 2002) from prior standard
// algorithm knowledge, with float32 coordinates. No Minecraft layout
// implementation was consulted. Vanilla's identity-based child set
// permits different sibling permutations on different JVM boots; use a stable
// resource ordering for the local drawing.
type advancementLayoutPoint struct{ x, y float32 }
type advancementLayoutVertex struct {
	id                           string
	parent, order                int
	children                     []int
	pre, modifier, shift, change float32
	thread, ancestor             int
}

func layoutAdvancementTree(root string, entries map[string]*advMeta, children map[string][]string) map[string]advancementLayoutPoint {
	result := map[string]advancementLayoutPoint{}
	if entries[root] == nil || !entries[root].hasDisplay {
		return result
	}
	var vertices []advancementLayoutVertex
	var visibleChildren func(string) []string
	visibleChildren = func(id string) []string {
		ids := append([]string(nil), children[id]...)
		sort.Strings(ids)
		var out []string
		for _, child := range ids {
			if entries[child].hasDisplay {
				out = append(out, child)
			} else {
				out = append(out, visibleChildren(child)...)
			}
		}
		return out
	}
	var build func(string, int, int) int
	build = func(id string, parent, order int) int {
		index := len(vertices)
		vertices = append(vertices, advancementLayoutVertex{id: id, parent: parent, order: order, thread: -1, ancestor: index})
		for order, child := range visibleChildren(id) {
			childIndex := build(child, index, order)
			vertices[index].children = append(vertices[index].children, childIndex)
		}
		return index
	}
	build(root, -1, 0)
	leftSibling := func(index int) int {
		v := vertices[index]
		if v.parent < 0 || v.order == 0 {
			return -1
		}
		return vertices[v.parent].children[v.order-1]
	}
	leftEdge := func(index int) int {
		if index < 0 {
			return -1
		}
		v := vertices[index]
		if len(v.children) > 0 {
			return v.children[0]
		}
		return v.thread
	}
	rightEdge := func(index int) int {
		if index < 0 {
			return -1
		}
		v := vertices[index]
		if len(v.children) > 0 {
			return v.children[len(v.children)-1]
		}
		return v.thread
	}
	move := func(left, right int, distance float32) {
		span := vertices[right].order - vertices[left].order
		if span <= 0 {
			return
		}
		share := distance / float32(span)
		vertices[right].change -= share
		vertices[right].shift += distance
		vertices[left].change += share
		vertices[right].pre += distance
		vertices[right].modifier += distance
	}
	apportion := func(index, defaultAncestor int) int {
		sibling := leftSibling(index)
		if sibling < 0 {
			return defaultAncestor
		}
		insideRight, outsideRight, insideLeft := index, index, sibling
		outsideLeft := vertices[vertices[index].parent].children[0]
		rightInnerMod, rightOuterMod := vertices[insideRight].modifier, vertices[outsideRight].modifier
		leftInnerMod, leftOuterMod := vertices[insideLeft].modifier, vertices[outsideLeft].modifier
		for rightEdge(insideLeft) >= 0 && leftEdge(insideRight) >= 0 {
			insideLeft = rightEdge(insideLeft)
			insideRight = leftEdge(insideRight)
			outsideLeft = leftEdge(outsideLeft)
			outsideRight = rightEdge(outsideRight)
			vertices[outsideRight].ancestor = index
			distance := (vertices[insideLeft].pre + leftInnerMod) - (vertices[insideRight].pre + rightInnerMod) + 1
			if distance > 0 {
				ancestor := vertices[insideLeft].ancestor
				if vertices[ancestor].parent != vertices[index].parent {
					ancestor = defaultAncestor
				}
				move(ancestor, index, distance)
				rightInnerMod += distance
				rightOuterMod += distance
			}
			leftInnerMod += vertices[insideLeft].modifier
			rightInnerMod += vertices[insideRight].modifier
			leftOuterMod += vertices[outsideLeft].modifier
			rightOuterMod += vertices[outsideRight].modifier
		}
		if next := rightEdge(insideLeft); next >= 0 && rightEdge(outsideRight) < 0 {
			vertices[outsideRight].thread = next
			vertices[outsideRight].modifier += leftInnerMod - rightOuterMod
		}
		if next := leftEdge(insideRight); next >= 0 && leftEdge(outsideLeft) < 0 {
			vertices[outsideLeft].thread = next
			vertices[outsideLeft].modifier += rightInnerMod - leftOuterMod
			defaultAncestor = index
		}
		return defaultAncestor
	}
	var place func(int)
	place = func(index int) {
		sibling := leftSibling(index)
		if len(vertices[index].children) == 0 {
			if sibling >= 0 {
				vertices[index].pre = vertices[sibling].pre + 1
			}
			return
		}
		kids := vertices[index].children
		ancestor := kids[0]
		for _, child := range kids {
			place(child)
			ancestor = apportion(child, ancestor)
		}
		var shift, change float32
		for i := len(kids) - 1; i >= 0; i-- {
			child := kids[i]
			vertices[child].pre += shift
			vertices[child].modifier += shift
			change += vertices[child].change
			shift += vertices[child].shift + change
		}
		center := (vertices[kids[0]].pre + vertices[kids[len(kids)-1]].pre) / 2
		if sibling >= 0 {
			vertices[index].pre = vertices[sibling].pre + 1
			vertices[index].modifier = vertices[index].pre - center
		} else {
			vertices[index].pre = center
		}
	}
	place(0)
	minimum := float32(0)
	var coordinates func(int, float32, int)
	coordinates = func(index int, modifier float32, depth int) {
		vertex := vertices[index]
		point := advancementLayoutPoint{float32(depth), vertex.pre + modifier}
		result[vertex.id] = point
		minimum = min(minimum, point.y)
		for _, child := range vertex.children {
			coordinates(child, modifier+vertex.modifier, depth+1)
		}
	}
	coordinates(0, 0, 0)
	if minimum < 0 {
		for id, point := range result {
			point.y -= minimum
			result[id] = point
		}
	}
	return result
}
