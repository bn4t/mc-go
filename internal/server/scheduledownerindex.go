package server

type ownerTickDeadline struct {
	id uint64
	at int64
}

// Ordinary owners have one inline record. Only saved duplicates (or requests
// accepted after their membership was cleared) allocate an additional slice.
type ownerTickDeadlines struct {
	first   ownerTickDeadline
	extra   []ownerTickDeadline
	minimum int64
}

// Caller holds the scheduler lock. Queries stay O(1); removals inspect only
// records of the same owner, never unrelated pending callbacks.
func (t *tickScheduler) indexTick(k schedKey, at int64) {
	owner := k
	owner.recordID = 0
	index, exists := t.ownerDeadlines[owner]
	record := ownerTickDeadline{id: k.recordID, at: at}
	if !exists {
		index = ownerTickDeadlines{first: record, minimum: at}
	} else {
		index.extra = append(index.extra, record)
		if at < index.minimum {
			index.minimum = at
		}
	}
	t.ownerDeadlines[owner] = index
}

func (t *tickScheduler) unindexTick(k schedKey) {
	owner := k
	owner.recordID = 0
	index, exists := t.ownerDeadlines[owner]
	if !exists {
		return
	}
	if index.first.id == k.recordID {
		if len(index.extra) == 0 {
			delete(t.ownerDeadlines, owner)
			return
		}
		index.first = index.extra[len(index.extra)-1]
		index.extra = index.extra[:len(index.extra)-1]
	} else {
		for i, record := range index.extra {
			if record.id == k.recordID {
				index.extra[i] = index.extra[len(index.extra)-1]
				index.extra = index.extra[:len(index.extra)-1]
				break
			}
		}
	}
	index.minimum = index.first.at
	for _, record := range index.extra {
		if record.at < index.minimum {
			index.minimum = record.at
		}
	}
	t.ownerDeadlines[owner] = index
}
