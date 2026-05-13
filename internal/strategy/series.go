package strategy

import "math"

func pushCapFloat(xs *[]float64, v float64, max int) {
	if max <= 0 {
		return
	}
	*xs = append(*xs, v)
	if len(*xs) > max {
		*xs = (*xs)[len(*xs)-max:]
	}
}

func meanStd(xs []float64) (mean float64, std float64, ok bool) {
	n := len(xs)
	if n < 2 {
		return 0, 0, false
	}
	var sum, sumsq float64
	for _, x := range xs {
		sum += x
		sumsq += x * x
	}
	mean = sum / float64(n)
	v := sumsq/float64(n) - mean*mean
	if v < 0 {
		v = 0
	}
	std = math.Sqrt(v)
	if std < 1e-12 {
		return mean, 0, false
	}
	return mean, std, true
}

// olsSlopeXY fits x = alpha + beta*y (minimize vertical error in x), returns beta.
func olsSlopeXY(x, y []float64) (beta float64, ok bool) {
	n := len(x)
	if n != len(y) || n < 3 {
		return 0, false
	}
	var sumY, sumX, sumYY, sumXY float64
	for i := 0; i < n; i++ {
		sumY += y[i]
		sumX += x[i]
		sumYY += y[i] * y[i]
		sumXY += x[i] * y[i]
	}
	fy := sumY / float64(n)
	fx := sumX / float64(n)
	var syy, sxy float64
	for i := 0; i < n; i++ {
		dy := y[i] - fy
		dx := x[i] - fx
		syy += dy * dy
		sxy += dx * dy
	}
	if syy < 1e-18 {
		return 0, false
	}
	return sxy / syy, true
}
