package collector

import (
	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/runtime/options"
)

type Collection struct {
	CollectionName string
	metrics        *shrinkingmap.ShrinkingMap[string, *Metric]
}

func NewCollection(name string, opts ...options.Option[Collection]) *Collection {
	return options.Apply(&Collection{
		CollectionName: name,
		metrics:        shrinkingmap.New[string, *Metric](),
	}, opts, func(collection *Collection) {
		collection.metrics.ForEach(func(_ string, metric *Metric) bool {
			metric.Namespace = collection.CollectionName
			metric.initPromMetric()

			return true
		})
	})
}

func (c *Collection) GetMetric(metricName string) *Metric {
	metric, exists := c.metrics.Get(metricName)
	if !exists {
		return nil
	}

	return metric
}

func (c *Collection) addMetric(metric *Metric) {
	if metric != nil {
		c.metrics.Set(metric.Name, metric)
	}
}

func WithMetric(metric *Metric) options.Option[Collection] {
	return func(c *Collection) {
		c.addMetric(metric)
	}
}
