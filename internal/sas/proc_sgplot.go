package sas

func init() {
	RegisterProc("SGPLOT", sgRunPlot)
}

// sgRunPlot implements PROC SGPLOT: one chart per BY group.
func sgRunPlot(s *Session, p *ProcStep) error {
	d, err := s.ProcData(p)
	if err != nil {
		return err
	}
	return sgByLoop(s, p, d, func(rows [][]Value) error {
		e, err := sgNewEnv(s, d.Subset(rows), p.Body, p.Opts)
		if err != nil {
			return err
		}
		if len(e.plots) == 0 {
			return nil
		}
		c, err := e.build(rows)
		if err != nil {
			return err
		}
		s.Out.AddGraph(&Graph{ODSName: "SGPlot", SVG: c.SVG()})
		return nil
	})
}
