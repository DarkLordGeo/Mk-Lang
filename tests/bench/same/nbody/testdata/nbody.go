// nbody, Go: the reference algorithm of ../README.md.
// Five bodies in seven [5]float64 arrays of one struct, indexed s.x[i]
// (bounds checked), every operation in the README's order; math.Sqrt (one
// SQRTSD). Every product that meets an addition is written float64(a*b),
// which the Go spec forbids fusing; at GOAMD64=v2 there is no FMA
// anyway. int64() truncates toward zero. Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
)

const bodies = 5
const dt = 0.0078125

type state struct {
	x, y, z, vx, vy, vz, m [bodies]float64
}

func energy(s *state) float64 {
	e := 0.0
	for i := 0; i < bodies; i++ {
		e = e + float64(0.5*s.m[i])*(float64(float64(s.vx[i]*s.vx[i])+float64(s.vy[i]*s.vy[i]))+float64(s.vz[i]*s.vz[i]))
		for j := i + 1; j < bodies; j++ {
			dx := s.x[i] - s.x[j]
			dy := s.y[i] - s.y[j]
			dz := s.z[i] - s.z[j]
			e = e - float64(s.m[i]*s.m[j])/math.Sqrt(float64(float64(dx*dx)+float64(dy*dy))+float64(dz*dz))
		}
	}
	return e
}

func advance(s *state, steps int) {
	for k := 0; k < steps; k++ {
		for i := 0; i < bodies; i++ {
			for j := i + 1; j < bodies; j++ {
				dx := s.x[i] - s.x[j]
				dy := s.y[i] - s.y[j]
				dz := s.z[i] - s.z[j]
				d2 := float64(float64(dx*dx)+float64(dy*dy)) + float64(dz*dz)
				mag := dt / float64(d2*math.Sqrt(d2))
				s.vx[i] = s.vx[i] - float64(float64(dx*s.m[j])*mag)
				s.vy[i] = s.vy[i] - float64(float64(dy*s.m[j])*mag)
				s.vz[i] = s.vz[i] - float64(float64(dz*s.m[j])*mag)
				s.vx[j] = s.vx[j] + float64(float64(dx*s.m[i])*mag)
				s.vy[j] = s.vy[j] + float64(float64(dy*s.m[i])*mag)
				s.vz[j] = s.vz[j] + float64(float64(dz*s.m[i])*mag)
			}
		}
		for i := 0; i < bodies; i++ {
			s.x[i] = s.x[i] + float64(dt*s.vx[i])
			s.y[i] = s.y[i] + float64(dt*s.vy[i])
			s.z[i] = s.z[i] + float64(dt*s.vz[i])
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: nbody <steps>")
		os.Exit(2)
	}
	steps, _ := strconv.Atoi(os.Args[1])
	s := state{
		x:  [bodies]float64{0.0, 4.0, -2.0, 6.0, -9.0},
		y:  [bodies]float64{0.0, 0.0, 0.0, 0.0, 0.0},
		z:  [bodies]float64{0.0, 0.25, -0.125, 0.5, 0.0},
		vx: [bodies]float64{0.0, 0.0, 0.0, 0.0, 0.0},
		vy: [bodies]float64{0.0, 1.375, -2.0, 1.125, -0.9375},
		vz: [bodies]float64{0.0, 0.0, 0.0625, 0.0, 0.03125},
		m:  [bodies]float64{8.0, 0.03125, 0.015625, 0.0625, 0.0078125},
	}
	advance(&s, steps)
	fmt.Println(int64(energy(&s) * 1e9))
}
