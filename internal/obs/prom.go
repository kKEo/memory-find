package obs

import (
	"bufio"
	"io"
	"math"
	"strconv"
	"strings"
)

// WriteText renders a snapshot in the Prometheus text exposition format
// (version 0.0.4): HELP and TYPE lines per family, families and series
// sorted, histograms as cumulative _bucket/_sum/_count series.
func WriteText(w io.Writer, snap Snapshot) error {
	bw := bufio.NewWriter(w)
	line := func(parts ...string) {
		for _, p := range parts {
			_, _ = bw.WriteString(p) // errors surface from Flush
		}
		_ = bw.WriteByte('\n')
	}
	for _, f := range snap.Families {
		if f.Help != "" {
			line("# HELP ", f.Name, " ", escapeHelp(f.Help))
		}
		line("# TYPE ", f.Name, " ", string(f.Kind))
		for _, s := range f.Series {
			switch f.Kind {
			case HistogramKind:
				for _, b := range s.Buckets {
					line(f.Name, "_bucket", labelsText(append(append([]Label(nil), s.Labels...), Label{Name: "le", Value: formatLe(b.Le)})), " ", strconv.FormatUint(b.Count, 10))
				}
				line(f.Name, "_sum", labelsText(s.Labels), " ", formatValue(s.Sum))
				line(f.Name, "_count", labelsText(s.Labels), " ", strconv.FormatUint(s.Count, 10))
			default:
				line(f.Name, labelsText(s.Labels), " ", formatValue(s.Value))
			}
		}
	}
	return bw.Flush()
}

func labelsText(ls []Label) string {
	if len(ls) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteByte('{')
	for i, l := range ls {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(l.Name + `="` + escapeLabel(l.Value) + `"`)
	}
	sb.WriteByte('}')
	return sb.String()
}

func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

func formatLe(f float64) string {
	if math.IsInf(f, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func formatValue(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	case math.IsNaN(f):
		return "NaN"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
