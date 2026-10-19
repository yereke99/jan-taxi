package pricing

// Fare is the set of fares a passenger may choose from for one route.
type Fare struct {
	Recommended int   `json:"recommended"`
	Min         int   `json:"min"`
	Max         int   `json:"max"`
	Step        int   `json:"step"`
	Options     []int `json:"options"`
}

// Compute prices a route with tariff t:
//
//	raw = base_fare + ceil(per_km × km) + ceil(per_minute × min)
//	      + extra_stop_fee × intermediate stops + Σ extra_fees.amount
//	min = clamp(raw rounded up to fare_step, min_fare, max_fare)
//
// The recommended fare equals min; the passenger may raise the fare in
// fare_step increments up to max_fare. Integer arithmetic only.
func Compute(t Tariff, distanceM, durationS, stopCount int) Fare {
	raw := int64(t.BaseFare)
	raw += ceilDiv(int64(t.PerKm)*int64(max(distanceM, 0)), 1000)
	raw += ceilDiv(int64(t.PerMinute)*int64(max(durationS, 0)), 60)
	raw += int64(t.ExtraStopFee) * int64(max(stopCount-2, 0))
	for _, f := range t.ExtraFees {
		raw += int64(max(f.Amount, 0))
	}

	step := int64(max(t.FareStep, 1))
	lo := clamp(ceilDiv(raw, step)*step, int64(t.MinFare), int64(t.MaxFare))
	f := Fare{Recommended: int(lo), Min: int(lo), Max: t.MaxFare, Step: int(step)}
	f.Options = Options(f.Min, f.Max, f.Step)
	return f
}

// Options lists every fare from lo to hi inclusive in steps of step.
func Options(lo, hi, step int) []int {
	if step <= 0 || hi < lo {
		return []int{}
	}
	out := make([]int, 0, (hi-lo)/step+1)
	for v := lo; v <= hi; v += step {
		out = append(out, v)
	}
	return out
}

// Commission returns the platform commission for fare at bps basis points,
// rounded half up: (fare × bps + 5000) / 10000 (ARCHITECTURE §6).
func Commission(fare, bps int) int {
	if fare <= 0 || bps <= 0 {
		return 0
	}
	return int((int64(fare)*int64(bps) + 5000) / 10000)
}

// OrderCommission is the commission snapshotted on a new order (ARCHITECTURE
// §22.2): with mode "fixed" the fixed amount, capped at the fare; otherwise the
// tariff percentage (Commission).
func OrderCommission(mode string, fixedAmount int64, fare, bps int) int {
	if mode == "fixed" {
		return int(max(min(fixedAmount, int64(max(fare, 0))), 0))
	}
	return Commission(fare, bps)
}

// ceilDiv divides non-negative a by positive b, rounding up.
func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func clamp(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
