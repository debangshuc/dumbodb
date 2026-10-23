// Copyright 2026 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package operators

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/dumbodb/internal/handler/handlererrors"
	"github.com/dolthub/dumbodb/internal/types"
	"github.com/dolthub/dumbodb/internal/util/must"
)

func evalOperator(t *testing.T, name string, arg any) (any, error) {
	t.Helper()
	op, err := NewOperator(must.NotFail(types.NewDocument(name, arg)))
	require.NoError(t, err)
	return op.Process(types.MakeDocument(0))
}

// Before the fix these operators returned wrong values where MongoDB 8.0
// errors or computes another value (expected results from mongod 8.0.28):
// overflow wrapped, non-numeric operands became 0, domain errors became NaN
// or Inf, and Decimal128 operands were treated as 0.
func TestMathOperatorsMatchMongoDB(t *testing.T) {
	date := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	for name, tc := range map[string]struct {
		op   string
		arg  any
		want any
	}{
		"subtract long overflow":  {"$subtract", must.NotFail(types.NewArray(int64(math.MinInt64), int64(1))), float64(math.MinInt64) - 1},
		"subtract int overflow":   {"$subtract", must.NotFail(types.NewArray(int32(math.MinInt32), int32(1))), int64(math.MinInt32) - 1},
		"subtract decimal":        {"$subtract", must.NotFail(types.NewArray(decimal(t, "5.5"), int32(1))), decimal(t, "4.5")},
		"subtract double decimal": {"$subtract", must.NotFail(types.NewArray(5.5, decimal(t, "0.5"))), decimal(t, "5.00000000000000")},
		"subtract dates":          {"$subtract", must.NotFail(types.NewArray(date, date.Add(-24*time.Hour))), int64(86400000)},
		"subtract from date":      {"$subtract", must.NotFail(types.NewArray(date, 1000.6)), date.Add(-1001 * time.Millisecond)},
		"abs min int":             {"$abs", int32(math.MinInt32), int64(2147483648)},
		"abs decimal":             {"$abs", decimal(t, "-2.5"), decimal(t, "2.5")},
		"ceil decimal":            {"$ceil", decimal(t, "2.1"), decimal(t, "3")},
		"sqrt decimal exact":      {"$sqrt", decimal(t, "100"), decimal(t, "10")},
		"sqrt decimal":            {"$sqrt", decimal(t, "2"), decimal(t, "1.414213562373095048801688724209698")},
		"exp decimal":             {"$exp", decimal(t, "1"), decimal(t, "2.718281828459045235360287471352662")},
		"ln decimal exact":        {"$ln", decimal(t, "1"), decimal(t, "0")},
		"log10 decimal inexact":   {"$log10", decimal(t, "2"), decimal(t, "0.3010299956639811952137388947244930")},
		"log decimal exact":       {"$log", must.NotFail(types.NewArray(decimal(t, "8"), decimal(t, "2"))), decimal(t, "3")},
		"pow decimal":             {"$pow", must.NotFail(types.NewArray(decimal(t, "2"), int32(3))), decimal(t, "8.000000000000000000000000000000000")},
		"pow int":                 {"$pow", must.NotFail(types.NewArray(int32(2), int32(10))), int32(1024)},
		"pow int to long":         {"$pow", must.NotFail(types.NewArray(int32(2), int32(40))), int64(1099511627776)},
		"pow long to min long":    {"$pow", must.NotFail(types.NewArray(int64(-2), int32(63))), int64(math.MinInt64)},
		"pow negative exponent":   {"$pow", must.NotFail(types.NewArray(int32(-1), int32(-5))), int32(-1)},
		"round int negative":      {"$round", must.NotFail(types.NewArray(int32(1250), int32(-2))), int32(1200)},
		"round int overflow":      {"$round", must.NotFail(types.NewArray(int32(math.MaxInt32), int32(-1))), int64(2147483650)},
		"trunc decimal place":     {"$trunc", must.NotFail(types.NewArray(decimal(t, "1299"), int32(-2))), decimal(t, "1.2E+3")},
		"round decimal half even": {"$round", decimal(t, "2.5"), decimal(t, "2")},
	} {
		got, err := evalOperator(t, tc.op, tc.arg)
		require.NoError(t, err, name)
		require.Equal(t, tc.want, got, name)
	}

	for name, tc := range map[string]struct {
		op   string
		arg  any
		code handlererrors.ErrorCode
	}{
		"abs min long":          {"$abs", int64(math.MinInt64), handlererrors.ErrAbsOfLongMin},
		"sqrt string":           {"$sqrt", "x", handlererrors.ErrMathNonNumeric},
		"sqrt negative":         {"$sqrt", int32(-1), handlererrors.ErrSqrtNegative},
		"sqrt negative decimal": {"$sqrt", decimal(t, "-1"), handlererrors.ErrSqrtNegative},
		"ln zero":               {"$ln", int32(0), handlererrors.ErrLnNonPositive},
		"log10 negative":        {"$log10", int32(-1), handlererrors.ErrLog10NonPositive},
		"log string":            {"$log", must.NotFail(types.NewArray("x", int32(10))), handlererrors.ErrLogNonNumericArgument},
		"log base one":          {"$log", must.NotFail(types.NewArray(int32(10), int32(1))), handlererrors.ErrLogInvalidBase},
		"pow zero negative":     {"$pow", must.NotFail(types.NewArray(int32(0), int32(-1))), handlererrors.ErrPowZeroNegativeExponent},
		"pow string":            {"$pow", must.NotFail(types.NewArray("x", int32(2))), handlererrors.ErrPowNonNumericBase},
		"trunc string":          {"$trunc", "x", handlererrors.ErrRoundNonNumeric},
		"round place range":     {"$round", must.NotFail(types.NewArray(1.5, int32(200))), handlererrors.ErrRoundPlaceOutOfRange},
		"round place fraction":  {"$round", must.NotFail(types.NewArray(1.55, 1.5)), handlererrors.ErrRoundPlaceNotIntegral},
		"subtract string":       {"$subtract", must.NotFail(types.NewArray("x", int32(1))), handlererrors.ErrTypeMismatch},
		"subtract date NaN":     {"$subtract", must.NotFail(types.NewArray(date, math.NaN())), handlererrors.ErrOverflow},
	} {
		_, err := evalOperator(t, tc.op, tc.arg)
		var cmdErr *handlererrors.CommandError
		require.True(t, errors.As(err, &cmdErr), "%s: %v", name, err)
		require.Equal(t, tc.code, cmdErr.Code(), "%s: %v", name, err)
	}
}

// Before the fix $divide and $mod by zero, and with non-numeric operands,
// failed as InternalError (or returned a double where MongoDB keeps a long);
// expected values from mongod 8.0.28.
func TestDivideAndModMatchMongoDB(t *testing.T) {
	arr := func(vs ...any) *types.Array { return must.NotFail(types.NewArray(vs...)) }

	for name, tc := range map[string]struct {
		op   string
		arg  any
		want any
	}{
		"divide ints":            {"$divide", arr(int32(6), int32(2)), float64(3)},
		"divide decimal inexact": {"$divide", arr(decimal(t, "1"), decimal(t, "3")), decimal(t, "0.3333333333333333333333333333333333")},
		"divide decimal exact":   {"$divide", arr(decimal(t, "6"), decimal(t, "2")), decimal(t, "3")},
		"divide double decimal":  {"$divide", arr(7.5, decimal(t, "2")), decimal(t, "3.75000000000000")},
		"divide NaN":             {"$divide", arr(math.NaN(), int32(2)), math.NaN()},
		"mod ints":               {"$mod", arr(int32(-7), int32(3)), int32(-1)},
		"mod int long":           {"$mod", arr(int32(7), int64(3)), int64(1)},
		"mod min long":           {"$mod", arr(int64(math.MinInt64), int64(-1)), int64(0)},
		"mod double":             {"$mod", arr(-7.5, int32(2)), -1.5},
		"mod decimal":            {"$mod", arr(decimal(t, "7.5"), int32(2)), decimal(t, "1.5")},
	} {
		got, err := evalOperator(t, tc.op, tc.arg)
		require.NoError(t, err, name)
		if f, ok := tc.want.(float64); ok && math.IsNaN(f) {
			require.True(t, math.IsNaN(got.(float64)), name)
			continue
		}
		require.Equal(t, tc.want, got, name)
	}

	for name, tc := range map[string]struct {
		op   string
		arg  any
		code handlererrors.ErrorCode
	}{
		"divide by zero":          {"$divide", arr(int32(7), int32(0)), handlererrors.ErrBadValue},
		"divide by negative zero": {"$divide", arr(int32(7), math.Copysign(0, -1)), handlererrors.ErrBadValue},
		"divide by decimal zero":  {"$divide", arr(int32(7), decimal(t, "0")), handlererrors.ErrBadValue},
		"divide string":           {"$divide", arr(int32(7), "x"), handlererrors.ErrTypeMismatch},
		"divide date":             {"$divide", arr(time.UnixMilli(1000), int32(2)), handlererrors.ErrTypeMismatch},
		"mod by zero":             {"$mod", arr(int32(7), int32(0)), handlererrors.ErrModByZero},
		"mod by double zero":      {"$mod", arr(7.5, 0.0), handlererrors.ErrModByZero},
		"mod by decimal zero":     {"$mod", arr(int32(7), decimal(t, "0")), handlererrors.ErrModByZeroDecimal},
		"mod string":              {"$mod", arr(int32(7), "x"), handlererrors.ErrModNonNumeric},
	} {
		_, err := evalOperator(t, tc.op, tc.arg)
		var cmdErr *handlererrors.CommandError
		require.True(t, errors.As(err, &cmdErr), "%s: %v", name, err)
		require.Equal(t, tc.code, cmdErr.Code(), "%s: %v", name, err)
	}
}
