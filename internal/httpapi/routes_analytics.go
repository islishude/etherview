package httpapi

func (h *Handler) registerAnalyticsRoutes() {
	h.handleBillable("getChartOverview", h.chartOverview)
	h.handleBillable("getChartMetric", h.chartMetric)
}
