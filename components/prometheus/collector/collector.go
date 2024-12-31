package collector

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/iotaledger/hive.go/ds/shrinkingmap"
)

// Collector is responsible for creation and collection of metrics for the prometheus.
type Collector struct {
	Registry    *prometheus.Registry
	collections *shrinkingmap.ShrinkingMap[string, *Collection]
}

// New creates an instance of Manager and creates a new prometheus registry for the protocol metrics collection.
func New() *Collector {
	return &Collector{
		Registry:    prometheus.NewRegistry(),
		collections: shrinkingmap.New[string, *Collection](),
	}
}

func (c *Collector) RegisterCollection(collection *Collection) {
	c.collections.Set(collection.CollectionName, collection)
	collection.metrics.ForEach(func(_ string, metric *Metric) bool {
		c.Registry.MustRegister(metric.promMetric)
		if metric.initValueFunc != nil {
			metricValue, labelValues := metric.initValueFunc()
			metric.update(metricValue, labelValues...)
		}
		if metric.initFunc != nil {
			metric.initFunc()
		}

		return true
	})
}

// Collect collects all metrics from the registered collections.
func (c *Collector) Collect() {
	c.collections.ForEach(func(_ string, collection *Collection) bool {
		collection.metrics.ForEach(func(_ string, metric *Metric) bool {
			metric.collect()
			return true
		})

		return true
	})
}

// Update updates the value of the existing metric defined by the subsystem and metricName.
// Note that the label values must be passed in the same order as they were defined in the metric, and must match the
// number of labels defined in the metric.
func (c *Collector) Update(subsystem string, metricName string, metricValue float64, labelValues ...string) {
	m := c.getMetric(subsystem, metricName)
	if m != nil {
		m.update(metricValue, labelValues...)
	}
}

// Increment increments the value of the existing metric defined by the subsystem and metricName.
// Note that the label values must be passed in the same order as they were defined in the metric, and must match the
// number of labels defined in the metric.
func (c *Collector) Increment(subsystem string, metricName string, labels ...string) {
	m := c.getMetric(subsystem, metricName)
	if m != nil {
		m.increment(labels...)
	}
}

// DeleteLabels deletes the metric with the given labels values.
func (c *Collector) DeleteLabels(subsystem string, metricName string, labelValues map[string]string) {
	m := c.getMetric(subsystem, metricName)
	if m != nil {
		m.deleteLabels(labelValues)
	}
}

// ResetMetric resets the metric with the given name.
func (c *Collector) ResetMetric(namespace string, metricName string) {
	m := c.getMetric(namespace, metricName)
	if m != nil {
		m.reset()
	}
}

func (c *Collector) Shutdown() {
	c.collections.ForEach(func(_ string, collection *Collection) bool {
		collection.metrics.ForEach(func(_ string, metric *Metric) bool {
			metric.shutdown()
			return true
		})

		return true
	})
}

func (c *Collector) getMetric(subsystem string, metricName string) *Metric {
	col := c.getCollection(subsystem)
	if col != nil {
		return col.GetMetric(metricName)
	}

	return nil
}

func (c *Collector) getCollection(subsystem string) *Collection {
	collection, exists := c.collections.Get(subsystem)
	if !exists {
		return nil
	}

	return collection
}
