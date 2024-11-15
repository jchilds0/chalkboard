package canvas

import (
	"github.com/gotk3/gotk3/cairo"
)

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
	if len(l.Points) < 4 {
		return
	}

	cr.SetSourceRGB(l.Pencil.Red, l.Pencil.Green, l.Pencil.Blue)
	cr.SetLineWidth(l.Pencil.Width)
	cr.SetLineJoin(cairo.LINE_JOIN_ROUND)

	cr.MoveTo(l.Points[0].X, l.Points[0].Y)
	for _, p := range l.Points {
		cr.LineTo(p.X, p.Y)
	}

	/*
		for i := 0; i < len(l.Points)-4; i++ {
			p0 := l.Points[i]
			p1 := l.Points[i+1]
			p2 := l.Points[i+2]
			p3 := l.Points[i+3]

			s := max(norm2(add(p2, scale(p1, -1))), 0.001)
			v1 := add(p1, scale(p0, -1))
			v2 := add(p2, scale(p3, -1))
			control1 := add(p1, scale(v1, 1/s))
			control2 := add(p2, scale(v2, 1/s))

			cr.MoveTo(p1.X, p1.Y)
			cr.CurveTo(control1.X, control1.Y, control2.X, control2.Y, p2.X, p2.Y)
		}
	*/

	cr.Stroke()
}
