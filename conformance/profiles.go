package conformance

// These are independent conformance layouts, measured from official vanilla
// oracles. They do not enable server admission for either protocol. The small
// source records and representative wire bodies live in testdata.
type javaProfile struct {
	protocol         int32
	loginSessionUUID bool
	joinOnlineMode   bool
}

func profileFor(protocol int32) (javaProfile, bool) {
	switch protocol {
	case 775:
		return javaProfile{protocol: 775}, true
	case 776:
		return javaProfile{protocol: 776, loginSessionUUID: true, joinOnlineMode: true}, true
	default:
		return javaProfile{}, false
	}
}
