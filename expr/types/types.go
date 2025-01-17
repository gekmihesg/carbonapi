package types

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-graphite/carbonapi/expr/consolidations"
	"github.com/go-graphite/carbonapi/expr/tags"
	pb "github.com/go-graphite/protocol/carbonapi_v3_pb"
	pickle "github.com/lomik/og-rek"
)

var (
	// ErrWildcardNotAllowed is an eval error returned when a wildcard/glob argument is found where a single series is required.
	ErrWildcardNotAllowed = errors.New("found wildcard where series expected")
	// ErrTooManyArguments is an eval error returned when too many arguments are provided.
	ErrTooManyArguments = errors.New("too many arguments")
)

// MetricData contains necessary data to represent parsed metric (ready to be send out or drawn)
type MetricData struct {
	pb.FetchResponse

	GraphOptions

	ValuesPerPoint    int
	aggregatedValues  []float64
	Tags              map[string]string
	AggregateFunction func([]float64) float64 `json:"-"`
}

// MarshalCSV marshals metric data to CSV
func MarshalCSV(results []*MetricData) []byte {

	var b []byte

	for _, r := range results {

		step := r.StepTime
		t := r.StartTime
		for _, v := range r.Values {
			b = append(b, '"')
			b = append(b, r.Name...)
			b = append(b, '"')
			b = append(b, ',')
			b = append(b, time.Unix(t, 0).Format("2006-01-02 15:04:05")...)
			b = append(b, ',')
			if !math.IsNaN(v) {
				b = strconv.AppendFloat(b, v, 'f', -1, 64)
			}
			b = append(b, '\n')
			t += step
		}
	}
	return b
}

// ConsolidateJSON consolidates values to maxDataPoints size
func ConsolidateJSON(maxDataPoints int, results []*MetricData) {
	startTime := results[0].StartTime
	endTime := results[0].StopTime
	for _, r := range results {
		t := r.StartTime
		if startTime > t {
			startTime = t
		}
		t = r.StopTime
		if endTime < t {
			endTime = t
		}
	}

	timeRange := endTime - startTime

	if timeRange <= 0 {
		return
	}

	for _, r := range results {
		numberOfDataPoints := math.Floor(float64(timeRange) / float64(r.StepTime))
		if numberOfDataPoints > float64(maxDataPoints) {
			valuesPerPoint := math.Ceil(numberOfDataPoints / float64(maxDataPoints))
			r.SetValuesPerPoint(int(valuesPerPoint))
		}
	}
}

// MarshalJSON marshals metric data to JSON
func MarshalJSON(results []*MetricData) []byte {
	var b []byte
	b = append(b, '[')

	var topComma bool
	for _, r := range results {
		if r == nil {
			continue
		}

		if topComma {
			b = append(b, ',')
		}
		topComma = true

		b = append(b, `{"target":`...)
		b = strconv.AppendQuoteToASCII(b, r.Name)
		b = append(b, `,"datapoints":[`...)

		var innerComma bool
		t := r.StartTime
		for _, v := range r.AggregatedValues() {
			if innerComma {
				b = append(b, ',')
			}
			innerComma = true

			b = append(b, '[')

			if math.IsInf(v, 0) || math.IsNaN(v) {
				b = append(b, "null"...)
			} else {
				b = strconv.AppendFloat(b, v, 'f', -1, 64)
			}

			b = append(b, ',')

			b = strconv.AppendInt(b, t, 10)

			b = append(b, ']')

			t += r.AggregatedTimeStep()
		}

		b = append(b, `],"tags":{`...)
		notFirstTag := false
		tags := make([]string, 0, len(r.Tags))
		for tag := range r.Tags {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		for _, tag := range tags {
			v := r.Tags[tag]
			if notFirstTag {
				b = append(b, ',')
			}
			b = strconv.AppendQuoteToASCII(b, tag)
			b = append(b, ':')
			b = strconv.AppendQuoteToASCII(b, v)
			notFirstTag = true
		}

		b = append(b, `}}`...)
	}

	b = append(b, ']')

	return b
}

// MarshalPickle marshals metric data to pickle format
func MarshalPickle(results []*MetricData) []byte {

	var p []map[string]interface{}

	for _, r := range results {
		values := make([]interface{}, len(r.Values))
		for i, v := range r.Values {
			if math.IsNaN(v) {
				values[i] = pickle.None{}
			} else {
				values[i] = v
			}

		}
		p = append(p, map[string]interface{}{
			"name":              r.Name,
			"pathExpression":    r.PathExpression,
			"consolidationFunc": r.ConsolidationFunc,
			"start":             r.StartTime,
			"end":               r.StopTime,
			"step":              r.StepTime,
			"xFilesFactor":      r.XFilesFactor,
			"values":            values,
		})
	}

	var buf bytes.Buffer

	penc := pickle.NewEncoder(&buf)
	penc.Encode(p)

	return buf.Bytes()
}

// MarshalProtobuf marshals metric data to protobuf
func MarshalProtobuf(results []*MetricData) ([]byte, error) {
	response := pb.MultiFetchResponse{}
	for _, metric := range results {
		response.Metrics = append(response.Metrics, (*metric).FetchResponse)
	}
	b, err := response.Marshal()
	if err != nil {
		return nil, err
	}

	return b, nil
}

// MarshalRaw marshals metric data to graphite's internal format, called 'raw'
func MarshalRaw(results []*MetricData) []byte {

	var b []byte

	for _, r := range results {

		b = append(b, r.Name...)

		b = append(b, ',')
		b = strconv.AppendInt(b, r.StartTime, 10)
		b = append(b, ',')
		b = strconv.AppendInt(b, r.StopTime, 10)
		b = append(b, ',')
		b = strconv.AppendInt(b, r.StepTime, 10)
		b = append(b, '|')

		var comma bool
		for _, v := range r.Values {
			if comma {
				b = append(b, ',')
			}
			comma = true
			if math.IsNaN(v) {
				b = append(b, "None"...)
			} else {
				b = strconv.AppendFloat(b, v, 'f', -1, 64)
			}
		}

		b = append(b, '\n')
	}
	return b
}

// SetValuesPerPoint sets value per point coefficient.
func (r *MetricData) SetValuesPerPoint(v int) {
	r.ValuesPerPoint = v
	r.aggregatedValues = nil
}

// AggregatedTimeStep aggregates time step
func (r *MetricData) AggregatedTimeStep() int64 {
	if r.ValuesPerPoint == 1 || r.ValuesPerPoint == 0 {
		return r.StepTime
	}

	return r.StepTime * int64(r.ValuesPerPoint)
}

// AggregatedValues aggregates values (with cache)
func (r *MetricData) AggregatedValues() []float64 {
	if r.aggregatedValues == nil {
		r.AggregateValues()
	}
	return r.aggregatedValues
}

// AggregateValues aggregates values
func (r *MetricData) AggregateValues() {
	if r.ValuesPerPoint == 1 || r.ValuesPerPoint == 0 {
		r.aggregatedValues = make([]float64, len(r.Values))
		copy(r.aggregatedValues, r.Values)
		return
	}

	if r.AggregateFunction == nil {
		var ok bool
		if r.AggregateFunction, ok = consolidations.ConsolidationToFunc[strings.ToLower(r.ConsolidationFunc)]; !ok {
			fmt.Printf("\nconsolidateFunc = %+v\n\nstack:\n%v\n\n", r.ConsolidationFunc, string(debug.Stack()))
		}
	}

	n := len(r.Values)/r.ValuesPerPoint + 1
	aggV := make([]float64, 0, n)

	v := r.Values

	for len(v) >= r.ValuesPerPoint {
		val := r.AggregateFunction(v[:r.ValuesPerPoint])
		aggV = append(aggV, val)
		v = v[r.ValuesPerPoint:]
	}

	if len(v) > 0 {
		val := r.AggregateFunction(v)
		aggV = append(aggV, val)
	}

	r.aggregatedValues = aggV
}

// MakeMetricData creates new metrics data with given metric timeseries
func MakeMetricData(name string, values []float64, step, start int64) *MetricData {
	return makeMetricDataWithTags(name, values, step, start, tags.ExtractTags(name))
}

// MakeMetricDataWithTags creates new metrics data with given metric Time Series (with tags)
func makeMetricDataWithTags(name string, values []float64, step, start int64, tags map[string]string) *MetricData {
	stop := start + int64(len(values))*step

	return &MetricData{FetchResponse: pb.FetchResponse{
		Name:      name,
		Values:    values,
		StartTime: start,
		StepTime:  step,
		StopTime:  stop,
	},
		Tags: tags,
	}
}
