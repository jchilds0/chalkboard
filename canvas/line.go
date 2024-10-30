package canvas

import "github.com/gotk3/gotk3/cairo"

type Pencil struct {
	Red   float64
	Green float64
	Blue  float64
	Width float64
}

type Line struct {
	Index  int
	Pencil Pencil
	Points []Point
}

type Point struct {
	X float64
	Y float64
}

func (l *Line) Draw(cr *cairo.Context) {
	if len(l.Points) == 0 {
		return
	}

	cr.SetSourceRGB(l.Pencil.Red, l.Pencil.Green, l.Pencil.Blue)

	start := l.Points[0]
	cr.MoveTo(start.X, start.Y)
	for _, p := range l.Points {
		cr.LineTo(p.X, p.Y)
	}

	cr.SetLineWidth(l.Pencil.Width)
	cr.Stroke()
}
