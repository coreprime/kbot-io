package scripting

import (
	"fmt"
	"math"
	"math/big"
	"strings"
)

// BOS source writes angles as <degrees> and distances as [units]. The
// compiler that built the retail scripts turns them into integers by
// multiplying the decimal value by 65536/360 (angles) or 163840 (distances)
// and truncating toward zero: <35> is 6371 and [2.4] is 393216.

const (
	angleNum  = 65536 // angle units per angleDen degrees
	angleDen  = 360
	linearNum = 163840 // linear units per BOS distance unit
	linearDen = 1

	angleMaxDecimals  = 3 // enough to name every integer angle value
	linearMaxDecimals = 6 // enough to name every integer linear value
)

// ParseAngleLiteral converts the decimal text of a <degrees> literal (for
// example "35" or "-12.5") to the integer angle the game uses.
func ParseAngleLiteral(text string) (int32, error) {
	return parseScaledLiteral(text, angleNum, angleDen, "<", ">")
}

// ParseLinearLiteral converts the decimal text of a [units] literal (for
// example "2.4") to the integer distance the game uses.
func ParseLinearLiteral(text string) (int32, error) {
	return parseScaledLiteral(text, linearNum, linearDen, "[", "]")
}

// AngleLiteral returns the shortest <degrees> literal that
// ParseAngleLiteral turns back into v.
func AngleLiteral(v int32) string {
	return "<" + formatScaledLiteral(v, angleNum, angleDen, angleMaxDecimals) + ">"
}

// LinearLiteral returns the shortest [units] literal that
// ParseLinearLiteral turns back into v.
func LinearLiteral(v int32) string {
	return "[" + formatScaledLiteral(v, linearNum, linearDen, linearMaxDecimals) + "]"
}

func parseScaledLiteral(text string, num, den int64, open, closing string) (int32, error) {
	s := strings.TrimSpace(text)
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg = true
		s = strings.TrimSpace(s[1:])
	case strings.HasPrefix(s, "+"):
		s = strings.TrimSpace(s[1:])
	}
	intPart, frac, _ := strings.Cut(s, ".")
	if intPart == "" && frac == "" {
		return 0, fmt.Errorf("empty %s%s literal", open, closing)
	}
	digits := intPart + frac
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("bad %s%s literal %q", open, closing, text)
		}
	}
	if digits == "" {
		return 0, fmt.Errorf("bad %s%s literal %q", open, closing, text)
	}
	k, _ := new(big.Int).SetString(digits, 10)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(frac))), nil)
	value := new(big.Int).Mul(k, big.NewInt(num))
	value.Quo(value, new(big.Int).Mul(scale, big.NewInt(den))) // truncates toward zero
	if neg {
		value.Neg(value)
	}
	if !value.IsInt64() || value.Int64() < math.MinInt32 || value.Int64() > math.MaxInt32 {
		return 0, fmt.Errorf("%s%s%s is out of the 32-bit range", open, strings.TrimSpace(text), closing)
	}
	return int32(value.Int64()), nil
}

func formatScaledLiteral(v int32, num, den int64, maxDecimals int) string {
	mag := int64(v)
	sign := ""
	if mag < 0 {
		mag = -mag
		sign = "-"
	}
	scale := int64(1)
	for p := 0; p <= maxDecimals; p++ {
		// Smallest k with trunc(k*num / (den*scale)) == mag.
		k := (mag*den*scale + num - 1) / num
		if k*num < (mag+1)*den*scale {
			whole, frac := k/scale, k%scale
			if p == 0 {
				return fmt.Sprintf("%s%d", sign, whole)
			}
			return fmt.Sprintf("%s%d.%0*d", sign, whole, p, frac)
		}
		scale *= 10
	}
	// Unreachable with the precisions above; fall back to an exact long form.
	k := (mag*den*scale + num - 1) / num
	return fmt.Sprintf("%s%d.%0*d", sign, k/scale, maxDecimals+1, k%scale)
}
