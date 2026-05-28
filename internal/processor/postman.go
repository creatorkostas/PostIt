package processor

import (
	"encoding/json"
	"postit/internal/models"
)

func ParsePostmanCollection(data []byte) (models.Collection, []models.RequestInfo, error) {
	var col models.Collection
	if err := json.Unmarshal(data, &col); err != nil {
		return col, nil, err
	}

	requests := []models.RequestInfo{}
	orderCounter := 0
	// Use the collection name as the top-level folder to avoid conflicts
	flattenItems(col.Item, col.Info.Name, []models.Event{}, &requests, &orderCounter)

	return col, requests, nil
}

func flattenItems(items []models.Item, prefix string, parentEvents []models.Event, result *[]models.RequestInfo, counter *int) {
	for _, item := range items {
		name := item.Name
		if prefix != "" {
			name = prefix + " > " + name
		}
		currentEvents := append([]models.Event{}, parentEvents...)
		currentEvents = append(currentEvents, item.Event...)
		if item.Request != nil {
			*result = append(*result, models.RequestInfo{
				Path:    name,
				Request: item.Request,
				Events:  currentEvents,
				Order:   *counter,
			})
			*counter++
		}
		if len(item.Item) > 0 {
			flattenItems(item.Item, name, currentEvents, result, counter)
		}
	}
}
