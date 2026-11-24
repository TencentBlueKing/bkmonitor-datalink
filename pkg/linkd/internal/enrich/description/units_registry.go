// Code generated from bk-monitor 51c834dc6 core/unit/init_data.py; DO NOT EDIT.
// Registry data only; runtime never executes upstream Python.
package description

func registeredUnit(id string) (unitDefinition, bool) {
	switch id {
	case "none", "Misc||none":
		return unitDefinition{factor: 1, index: 0, suffix: "", prefixes: []string{}}, true
	case "short", "Misc||short":
		return unitDefinition{factor: 1, index: 0, suffix: "", prefixes: []string{}}, true
	case "percent", "Misc||percent":
		return unitDefinition{factor: 100, index: 0, suffix: "", prefixes: []string{"%", "x100%"}, percent: true}, true
	case "percentunit", "Misc||percentunit":
		return unitDefinition{factor: 100, index: 1, suffix: "", prefixes: []string{"%", "x100%"}, percent: true}, true
	case "bits", "Data (IEC)||bits":
		return unitDefinition{factor: 1024, index: 0, suffix: "b", prefixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}}, true
	case "bytes", "Data (IEC)||bytes":
		return unitDefinition{factor: 1024, index: 0, suffix: "B", prefixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}}, true
	case "kbytes", "Data (IEC)||kbytes":
		return unitDefinition{factor: 1024, index: 1, suffix: "B", prefixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}}, true
	case "mbytes", "Data (IEC)||mbytes":
		return unitDefinition{factor: 1024, index: 2, suffix: "B", prefixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}}, true
	case "gbytes", "Data (IEC)||gbytes":
		return unitDefinition{factor: 1024, index: 3, suffix: "B", prefixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}}, true
	case "tbytes", "Data (IEC)||tbytes":
		return unitDefinition{factor: 1024, index: 4, suffix: "B", prefixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}}, true
	case "pbytes", "Data (IEC)||pbytes":
		return unitDefinition{factor: 1024, index: 5, suffix: "B", prefixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}}, true
	case "decbits", "Data (Metric)||decbits":
		return unitDefinition{factor: 1000, index: 0, suffix: "b", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "decbytes", "Data (Metric)||decbytes":
		return unitDefinition{factor: 1000, index: 0, suffix: "B", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "deckbytes", "Data (Metric)||deckbytes":
		return unitDefinition{factor: 1000, index: 1, suffix: "B", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "decmbytes", "Data (Metric)||decmbytes":
		return unitDefinition{factor: 1000, index: 2, suffix: "B", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "decgbytes", "Data (Metric)||decgbytes":
		return unitDefinition{factor: 1000, index: 3, suffix: "B", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "dectbytes", "Data (Metric)||dectbytes":
		return unitDefinition{factor: 1000, index: 4, suffix: "B", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "decpbytes", "Data (Metric)||decpbytes":
		return unitDefinition{factor: 1000, index: 5, suffix: "B", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "pps", "Data Rate||pps":
		return unitDefinition{factor: 1000, index: 0, suffix: "pps", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "bps", "Data Rate||bps":
		return unitDefinition{factor: 1000, index: 0, suffix: "bps", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "Bps", "Data Rate||Bps":
		return unitDefinition{factor: 1000, index: 0, suffix: "Bs", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "KBs", "Data Rate||KBs":
		return unitDefinition{factor: 1000, index: 1, suffix: "Bs", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "Kbits", "Data Rate||Kbits":
		return unitDefinition{factor: 1000, index: 1, suffix: "bps", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "MBs", "Data Rate||MBs":
		return unitDefinition{factor: 1000, index: 2, suffix: "Bs", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "Mbits", "Data Rate||Mbits":
		return unitDefinition{factor: 1000, index: 2, suffix: "bps", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "GBs", "Data Rate||GBs":
		return unitDefinition{factor: 1000, index: 3, suffix: "Bs", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "Gbits", "Data Rate||Gbits":
		return unitDefinition{factor: 1000, index: 3, suffix: "bps", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "TBs", "Data Rate||TBs":
		return unitDefinition{factor: 1000, index: 4, suffix: "Bs", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "Tbits", "Data Rate||Tbits":
		return unitDefinition{factor: 1000, index: 4, suffix: "bps", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "PBs", "Data Rate||PBs":
		return unitDefinition{factor: 1000, index: 5, suffix: "Bs", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "Pbits", "Data Rate||Pbits":
		return unitDefinition{factor: 1000, index: 5, suffix: "bps", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "celsius", "Temperature||celsius":
		return unitDefinition{factor: 1, index: 0, suffix: "°C", prefixes: []string{}}, true
	case "fahrenheit", "Temperature||fahrenheit":
		return unitDefinition{factor: 1, index: 0, suffix: "°F", prefixes: []string{}}, true
	case "kelvin", "Temperature||kelvin":
		return unitDefinition{factor: 1, index: 0, suffix: "K", prefixes: []string{}}, true
	case "hertz", "Time||hertz":
		return unitDefinition{factor: 1000, index: 0, suffix: "Hz", prefixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}}, true
	case "ns", "Time||ns":
		return unitDefinition{factor: 1000, index: 0, suffix: "", prefixes: []string{"ns", "µs", "ms", "s", "m", "h", "d"}, factors: map[string]float64{"m": 60, "h": 60, "d": 24}}, true
	case "µs", "Time||µs":
		return unitDefinition{factor: 1000, index: 1, suffix: "", prefixes: []string{"ns", "µs", "ms", "s", "m", "h", "d"}, factors: map[string]float64{"m": 60, "h": 60, "d": 24}}, true
	case "ms", "Time||ms":
		return unitDefinition{factor: 1000, index: 2, suffix: "", prefixes: []string{"ns", "µs", "ms", "s", "m", "h", "d"}, factors: map[string]float64{"m": 60, "h": 60, "d": 24}}, true
	case "s", "Time||s":
		return unitDefinition{factor: 1000, index: 3, suffix: "", prefixes: []string{"ns", "µs", "ms", "s", "m", "h", "d"}, factors: map[string]float64{"m": 60, "h": 60, "d": 24}}, true
	case "m", "Time||m":
		return unitDefinition{factor: 1000, index: 4, suffix: "", prefixes: []string{"ns", "µs", "ms", "s", "m", "h", "d"}, factors: map[string]float64{"m": 60, "h": 60, "d": 24}}, true
	case "h", "Time||h":
		return unitDefinition{factor: 1000, index: 5, suffix: "", prefixes: []string{"ns", "µs", "ms", "s", "m", "h", "d"}, factors: map[string]float64{"m": 60, "h": 60, "d": 24}}, true
	case "d", "Time||d":
		return unitDefinition{factor: 1000, index: 6, suffix: "", prefixes: []string{"ns", "µs", "ms", "s", "m", "h", "d"}, factors: map[string]float64{"m": 60, "h": 60, "d": 24}}, true
	case "cps", "Throughput||cps":
		return unitDefinition{factor: 1000, index: 0, suffix: "cps", prefixes: []string{"", "K", "M", "B", "T"}}, true
	case "ops", "Throughput||ops":
		return unitDefinition{factor: 1000, index: 0, suffix: "ops", prefixes: []string{"", "K", "M", "B", "T"}}, true
	case "reqps", "Throughput||reqps":
		return unitDefinition{factor: 1000, index: 0, suffix: "reqps", prefixes: []string{"", "K", "M", "B", "T"}}, true
	case "rps", "Throughput||rps":
		return unitDefinition{factor: 1000, index: 0, suffix: "rps", prefixes: []string{"", "K", "M", "B", "T"}}, true
	case "wps", "Throughput||wps":
		return unitDefinition{factor: 1000, index: 0, suffix: "wps", prefixes: []string{"", "K", "M", "B", "T"}}, true
	case "iops", "Throughput||iops":
		return unitDefinition{factor: 1000, index: 0, suffix: "iops", prefixes: []string{"", "K", "M", "B", "T"}}, true
	case "cpm", "Throughput||cpm":
		return unitDefinition{factor: 1000, index: 0, suffix: "cpm", prefixes: []string{"", "K", "M", "B", "T"}}, true
	case "opm", "Throughput||opm":
		return unitDefinition{factor: 1000, index: 0, suffix: "opm", prefixes: []string{"", "K", "M", "B", "T"}}, true
	case "rpm", "Throughput||rpm":
		return unitDefinition{factor: 1000, index: 0, suffix: "rpm", prefixes: []string{"", "K", "M", "B", "T"}}, true
	case "wpm", "Throughput||wpm":
		return unitDefinition{factor: 1000, index: 0, suffix: "wpm", prefixes: []string{"", "K", "M", "B", "T"}}, true
	default:
		return unitDefinition{}, false
	}
}
