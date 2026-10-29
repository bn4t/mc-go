package server

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// Finite attribute values keep the established JSON number representation.
// JSON has no nonfinite numbers, so those carry their exact IEEE64 bits instead;
// native NBT continues to carry the original double without this wrapper.
type playerAttributeJSONNumber float64

func (value playerAttributeJSONNumber) MarshalJSON() ([]byte, error) {
	number := float64(value)
	if !math.IsNaN(number) && !math.IsInf(number, 0) {
		return strconv.AppendFloat(nil, number, 'g', -1, 64), nil
	}
	return json.Marshal(struct {
		Bits string `json:"float64_bits"`
	}{fmt.Sprintf("%016x", math.Float64bits(number))})
}
func (value *playerAttributeJSONNumber) UnmarshalJSON(raw []byte) error {
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		*value = playerAttributeJSONNumber(number)
		return nil
	}
	var tagged struct {
		Bits string `json:"float64_bits"`
	}
	if err := json.Unmarshal(raw, &tagged); err != nil {
		return err
	}
	if len(tagged.Bits) != 16 {
		return fmt.Errorf("invalid attribute float64_bits %q", tagged.Bits)
	}
	bits, err := strconv.ParseUint(tagged.Bits, 16, 64)
	if err != nil {
		return err
	}
	*value = playerAttributeJSONNumber(math.Float64frombits(bits))
	return nil
}
func (attribute playerAttributePersist) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name      string                           `json:"name"`
		Base      playerAttributeJSONNumber        `json:"base"`
		Modifiers []playerAttributeModifierPersist `json:"modifiers,omitempty"`
	}{attribute.Name, playerAttributeJSONNumber(attribute.Base), attribute.Modifiers})
}
func (attribute *playerAttributePersist) UnmarshalJSON(raw []byte) error {
	var record struct {
		Name      string                           `json:"name"`
		Base      playerAttributeJSONNumber        `json:"base"`
		Modifiers []playerAttributeModifierPersist `json:"modifiers,omitempty"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
	}
	attribute.Name, attribute.Base, attribute.Modifiers = record.Name, float64(record.Base), record.Modifiers
	return nil
}
func (modifier playerAttributeModifierPersist) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID        string                    `json:"id"`
		Amount    playerAttributeJSONNumber `json:"amount"`
		Operation int                       `json:"operation"`
	}{modifier.ID, playerAttributeJSONNumber(modifier.Amount), modifier.Operation})
}
func (modifier *playerAttributeModifierPersist) UnmarshalJSON(raw []byte) error {
	var record struct {
		ID        string                    `json:"id"`
		Amount    playerAttributeJSONNumber `json:"amount"`
		Operation int                       `json:"operation"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
	}
	modifier.ID, modifier.Amount, modifier.Operation = record.ID, float64(record.Amount), record.Operation
	return nil
}
