package description

import (
	"math"
	"strings"
)

const pointPrecision = 6

type unitDefinition struct {
	factor   float64
	index    int
	suffix   string
	prefixes []string
	factors  map[string]float64
	percent  bool
}

func loadUnit(id string) (unitDefinition, error) {
	if len(id) > 256 {
		return unitDefinition{}, invalid("unit_invalid")
	}
	if unit, ok := registeredUnit(id); ok {
		return unit, nil
	}
	if strings.Contains(id, "||") {
		return unitDefinition{}, invalid("unit_invalid")
	}
	// bk-monitor 将不在 registry 的普通 ID 解释为固定自定义单位。
	return unitDefinition{factor: 1, suffix: id}, nil
}

func (u unitDefinition) factorAt(index int) float64 {
	if index >= 0 && index < len(u.prefixes) {
		if value, ok := u.factors[u.prefixes[index]]; ok {
			return value
		}
	}
	return u.factor
}

func (u unitDefinition) format(number Number) (string, error) {
	value, index, integer := number.value, u.index, number.integer
	if len(u.prefixes) == 0 {
		return pythonNumber(roundDecimal(value, pointPrecision), integer) + u.suffix, nil
	}
	// Percent 不向更大的单位晋升，percentunit 仍降为百分数。
	lower, upper := 1.0, u.factorAt(index)
	if u.percent {
		lower, upper = math.Inf(1), math.Inf(1)
	}
	if math.Abs(value) < lower {
		for math.Abs(value) < lower && index > 0 {
			value *= u.factorAt(index - 1)
			index--
			if index <= 0 {
				break
			}
			lower = 1
		}
	} else if math.Abs(value) >= upper {
		for math.Abs(value) >= upper && index < len(u.prefixes)-1 {
			value /= u.factorAt(index + 1)
			integer = false
			index++
			if index >= len(u.prefixes)-1 {
				break
			}
			upper = u.factorAt(index + 1)
		}
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return "", invalid("unit_overflow")
	}
	return pythonNumber(roundDecimal(value, pointPrecision), integer) + u.prefixes[index] + u.suffix, nil
}

func (u unitDefinition) thresholdSuffix(prefix string) string {
	for _, candidate := range u.prefixes {
		if candidate == prefix {
			return prefix + u.suffix
		}
	}
	return u.suffix
}

func (u unitDefinition) toMinimum(value float64, index int) float64 {
	// 固定单位的 convert_to_max 直接返回来源值，不执行 round。
	if len(u.prefixes) == 0 {
		return value
	}
	for index > 0 {
		value *= u.factorAt(index)
		index--
	}
	return roundDecimal(value, pointPrecision)
}

func (u unitDefinition) thresholdToMinimum(value float64, prefix string) float64 {
	if len(u.prefixes) == 0 {
		return value
	}
	for index, candidate := range u.prefixes {
		if candidate == prefix {
			return u.toMinimum(value, index)
		}
	}
	return roundDecimal(value, pointPrecision)
}
