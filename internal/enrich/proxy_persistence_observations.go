package enrich

import "github.com/ethereum/go-ethereum/common"

type proxyObservationSet struct {
	code           map[common.Address]proxyCodeObservation
	beacons        map[common.Address]proxyBeaconObservation
	proxies        int
	rejected       int
	v2Differences  int
	uupsCompatible int
	uupsRejected   int
}

func collectProxyObservations(
	detections []proxyDetection,
	beacons []beaconDetection,
	uupsProbes []uupsImplementationProbeResult,
) (proxyObservationSet, error) {
	codeObservations := make(map[common.Address]proxyCodeObservation)
	beaconObservations := make(map[common.Address]proxyBeaconObservation)
	proxyCount, rejectedCount, v2DifferenceCount := 0, 0, 0
	uupsCompatibleCount, uupsRejectedCount := 0, 0
	for _, result := range uupsProbes {
		if err := mergeProxyCodeObservation(
			codeObservations, result.target.address, result.target.codeHash, result.code,
		); err != nil {
			return proxyObservationSet{}, Permanent(err)
		}
		if result.compatible() {
			uupsCompatibleCount++
		} else {
			uupsRejectedCount++
		}
	}
	for _, detection := range detections {
		if detection.v2Active && detection.v2.LegacyProjectionChanged {
			v2DifferenceCount++
		}
		if err := mergeProxyCodeObservation(codeObservations, detection.candidate.address, detection.codeHash, detection.code); err != nil {
			return proxyObservationSet{}, Permanent(err)
		}
		if detection.proxy == nil {
			if detection.rejected != "" {
				rejectedCount++
			}
			continue
		}
		proxyCount++
		if detection.exact != nil {
			if err := mergeProxyCodeObservation(
				codeObservations, detection.exact.implementation,
				detection.exact.implementationHash, detection.exact.implementationCode,
			); err != nil {
				return proxyObservationSet{}, Permanent(err)
			}
			if detection.exact.admin != nil {
				if err := mergeProxyCodeObservation(
					codeObservations, *detection.exact.admin,
					detection.exact.adminHash, detection.exact.adminCode,
				); err != nil {
					return proxyObservationSet{}, Permanent(err)
				}
			}
			if detection.exact.beacon != nil {
				if err := mergeProxyCodeObservation(
					codeObservations, *detection.exact.beacon,
					detection.exact.beaconHash, detection.exact.beaconCode,
				); err != nil {
					return proxyObservationSet{}, Permanent(err)
				}
				if err := mergeProxyBeaconObservation(beaconObservations, proxyBeaconObservation{
					address: *detection.exact.beacon, codeHash: detection.exact.beaconHash,
					implementation:     detection.exact.implementation,
					implementationHash: detection.exact.implementationHash,
					sources:            map[string]struct{}{"runtime_immutable": {}},
				}); err != nil {
					return proxyObservationSet{}, Permanent(err)
				}
			}
		}
		if err := mergeProxyCodeObservation(
			codeObservations, detection.proxy.implementation,
			detection.proxy.implementationHash, detection.proxy.implementationCode,
		); err != nil {
			return proxyObservationSet{}, Permanent(err)
		}
		if detection.proxy.admin != nil {
			if err := mergeProxyCodeObservation(
				codeObservations, *detection.proxy.admin,
				detection.proxy.adminHash, detection.proxy.adminCode,
			); err != nil {
				return proxyObservationSet{}, Permanent(err)
			}
		}
		if detection.proxy.beacon != nil {
			if err := mergeProxyCodeObservation(
				codeObservations, *detection.proxy.beacon,
				detection.proxy.beaconHash, detection.proxy.beaconCode,
			); err != nil {
				return proxyObservationSet{}, Permanent(err)
			}
			if err := mergeProxyBeaconObservation(beaconObservations, proxyBeaconObservation{
				address: *detection.proxy.beacon, codeHash: detection.proxy.beaconHash,
				implementation:     detection.proxy.implementation,
				implementationHash: detection.proxy.implementationHash,
				sources:            map[string]struct{}{"proxy_slot": {}},
			}); err != nil {
				return proxyObservationSet{}, Permanent(err)
			}
		}
	}
	for _, detection := range beacons {
		if err := mergeProxyCodeObservation(
			codeObservations, detection.candidate.address, detection.codeHash, detection.code,
		); err != nil {
			return proxyObservationSet{}, Permanent(err)
		}
		if detection.implementation == (common.Address{}) {
			if detection.rejected != "" {
				rejectedCount++
			}
			continue
		}
		if err := mergeProxyCodeObservation(
			codeObservations, detection.implementation,
			detection.implementationHash, detection.implementationCode,
		); err != nil {
			return proxyObservationSet{}, Permanent(err)
		}
		if err := mergeProxyBeaconObservation(beaconObservations, proxyBeaconObservation{
			address: detection.candidate.address, codeHash: detection.codeHash,
			implementation:     detection.implementation,
			implementationHash: detection.implementationHash,
			sources:            map[string]struct{}{"standalone_probe": {}},
		}); err != nil {
			return proxyObservationSet{}, Permanent(err)
		}
	}
	return proxyObservationSet{
		code: codeObservations, beacons: beaconObservations,
		proxies: proxyCount, rejected: rejectedCount, v2Differences: v2DifferenceCount,
		uupsCompatible: uupsCompatibleCount, uupsRejected: uupsRejectedCount,
	}, nil
}
