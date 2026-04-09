package notify

import (
	"bytes"
	"fmt"
	"image/color"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

// EquityPNG renders a simple equity curve as PNG bytes.
func EquityPNG(equity []float64) ([]byte, error) {
	if len(equity) == 0 {
		return nil, fmt.Errorf("empty equity")
	}
	p := plot.New()
	p.Title.Text = "Equity"
	p.X.Label.Text = "Bar"
	p.Y.Label.Text = "USD"
	pts := make(plotter.XYs, len(equity))
	for i, v := range equity {
		pts[i].X = float64(i)
		pts[i].Y = v
	}
	l, err := plotter.NewLine(pts)
	if err != nil {
		return nil, err
	}
	l.Color = color.RGBA{R: 33, G: 150, B: 243, A: 255}
	p.Add(l)
	w, h := 6*vg.Inch, 3*vg.Inch
	c, err := p.WriterTo(w, h, "png")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := c.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
