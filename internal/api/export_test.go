package api

import (
	"postit/internal/models"
	"testing"
)

func TestExportPostman(t *testing.T) {
	reqs := []models.RequestInfo{
		{
			Path: "Folder1 > Req1",
			Request: &models.Request{
				Method: "GET",
				URL:    models.URL{Raw: "https://example.com/1"},
			},
			Order: 0,
		},
		{
			Path: "Folder1 > SubFolder > Req2",
			Request: &models.Request{
				Method: "POST",
				URL:    models.URL{Raw: "https://example.com/2"},
			},
			Order: 1,
		},
		{
			Path: "Folder2 > Req3",
			Request: &models.Request{
				Method: "PUT",
				URL:    models.URL{Raw: "https://example.com/3"},
			},
			Order: 2,
		},
	}

	t.Run("Export All", func(t *testing.T) {
		col := ExportPostman(reqs, "")
		if col.Info.Name != "PostIt Export" {
			t.Errorf("Expected name PostIt Export, got %s", col.Info.Name)
		}
		if len(col.Item) != 2 { // Folder1 and Folder2
			t.Errorf("Expected 2 root items, got %d", len(col.Item))
		}
	})

	t.Run("Export Folder1", func(t *testing.T) {
		col := ExportPostman(reqs, "Folder1")
		if col.Info.Name != "Folder1" {
			t.Errorf("Expected name Folder1, got %s", col.Info.Name)
		}
		if len(col.Item) != 2 { // Req1 and SubFolder
			t.Errorf("Expected 2 root items in Folder1 export, got %d", len(col.Item))
		}
		
		// Check that Req1 is at root of exported collection
		foundReq1 := false
		for _, itm := range col.Item {
			if itm.Name == "Req1" && itm.Request != nil {
				foundReq1 = true
				break
			}
		}
		if !foundReq1 {
			t.Error("Expected Req1 at root of exported Folder1")
		}
	})

	t.Run("Export SubFolder", func(t *testing.T) {
		col := ExportPostman(reqs, "Folder1 > SubFolder")
		if col.Info.Name != "Folder1 > SubFolder" {
			t.Errorf("Expected name Folder1 > SubFolder, got %s", col.Info.Name)
		}
		if len(col.Item) != 1 { // Req2
			t.Errorf("Expected 1 item in SubFolder export, got %d", len(col.Item))
		}
		if col.Item[0].Name != "Req2" {
			t.Errorf("Expected Req2, got %s", col.Item[0].Name)
		}
	})
}
