package server

import (
	"errors"
	"fmt"
)

var ErrUnknownJavaStatistic = errors.New("unknown canonical statistic")

// Stat type and custom-stat IDs are unchanged in the official775/776 reports.
// The value field still belongs to a different registry for each category;
// block registry IDs are not block-state IDs. A valid newer counter may be
// absent from an older viewer's optional statistics screen and is omitted.
func (p *javaProfile) projectStatistic(key statKey) (statKey, bool, error) {
	if key.category < 0 || key.category >= statCategoryN {
		return statKey{}, false, fmt.Errorf("%w: category%d", ErrUnknownJavaStatistic, key.category)
	}
	domains := [statCategoryN]string{"minecraft:block", "minecraft:item", "minecraft:item", "minecraft:item", "minecraft:item", "minecraft:item", "minecraft:entity_type", "minecraft:entity_type", "minecraft:custom_stat"}
	domain := builtinDomain(p.version.Protocol, domains[key.category])
	if domain == nil || key.id < 0 || int64(key.id) >= int64(len(domain.from776)) {
		return statKey{}, false, fmt.Errorf("%w: category%d value%d", ErrUnknownJavaStatistic, key.category, key.id)
	}
	value := domain.from776[key.id]
	if value < 0 {
		return statKey{}, false, nil
	}
	category, err := p.projectBuiltinRegistryID("minecraft:stat_type", key.category)
	if err != nil {
		return statKey{}, false, err
	}
	return statKey{category: category, id: value}, true, nil
}
