package dashboardmetrics

import (
	"time"
)

func databaseSizesMetrics() *DatabaseSizesMetric {
	permanentSize := deps.Protocol.Engines.Main.Get().Storage.PermanentDatabaseSize()
	prunableSize := deps.Protocol.Engines.Main.Get().Storage.PrunableDatabaseSize()
	txRetainerSize := deps.Protocol.Engines.Main.Get().Storage.TransactionRetainerDatabaseSize()

	return &DatabaseSizesMetric{
		Permanent:  permanentSize,
		Prunable:   prunableSize,
		TxRetainer: txRetainerSize,
		Total:      permanentSize + prunableSize + txRetainerSize,
		Time:       time.Now().Unix(),
	}
}
