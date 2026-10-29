package server

import "testing"

func TestAttributeMetadataMatchesRegisteredDefaultsAndSyncPolicy(t *testing.T) {
	if len(attributeInfo) != 40 || len(attributeInfo) != len(attrRegisteredDefaults) {
		t.Fatalf("attribute registry counts: metadata=%d defaults=%d", len(attributeInfo), len(attrRegisteredDefaults))
	}
	for name, info := range attributeInfo {
		value, ok := attrRegisteredDefaults[name]
		if !ok || value != info.defaultValue {
			t.Errorf("%s registered default %v/%v, metadata %v", name, value, ok, info.defaultValue)
		}
		if attrSyncable[name] != info.clientSync {
			t.Errorf("%s sync policy differs from native metadata", name)
		}
	}
	for name := range attrSyncable {
		if _, ok := attributeInfo[name]; !ok {
			t.Errorf("unknown syncable attribute %s", name)
		}
	}
}
