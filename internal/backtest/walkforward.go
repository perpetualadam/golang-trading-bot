package backtest

// Window describes one walk-forward segment.
type Window struct {
	TrainStart int
	TrainEnd   int
	TestStart  int
	TestEnd    int
}

// WalkForward generates train/test index windows over total bars.
func WalkForward(total, trainBars, testBars, step int) []Window {
	if total <= 0 || trainBars <= 0 || testBars <= 0 || step <= 0 {
		return nil
	}
	var w []Window
	start := 0
	for {
		tr0 := start
		tr1 := start + trainBars
		te0 := tr1
		te1 := te0 + testBars
		if te1 > total {
			break
		}
		w = append(w, Window{TrainStart: tr0, TrainEnd: tr1, TestStart: te0, TestEnd: te1})
		start += step
		if start+trainBars+testBars > total {
			break
		}
	}
	return w
}
