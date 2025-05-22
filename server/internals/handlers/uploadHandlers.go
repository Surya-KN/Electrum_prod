package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"strconv"

	"github.com/AaronDennis07/electrum/internals/database"
	"github.com/AaronDennis07/electrum/internals/models"
	"github.com/gofiber/fiber/v2"
	"github.com/xuri/excelize/v2"
)

func UploadCourse(c *fiber.Ctx) error {
	courseFile, err := c.FormFile("courses")
	if err != nil {
		log.Printf("UploadCourse: Error getting form file 'courses': %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Error uploading course file"})
	}

	file, err := courseFile.Open()
	if err != nil {
		log.Printf("UploadCourse: Error opening uploaded file: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to open uploaded file"})
	}
	defer file.Close()

	f, err := excelize.OpenReader(file)
	if err != nil {
		log.Printf("UploadCourse: Error opening Excel reader: %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid Excel file format"})
	}

	cols, err := f.GetCols("Sheet1")
	if err != nil {
		log.Printf("UploadCourse: Error getting columns from Sheet1: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to process Excel sheet data"})
	}

	if len(cols) == 0 {
		log.Printf("UploadCourse: No columns found in Sheet1")
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Excel sheet is empty or has no columns"})
	}

	// Assuming the intent is to return the first column's data
	data := map[string]interface{}{
		"courseColumnData": cols[0], // Renamed for clarity
	}
	out, err := json.Marshal(data)
	if err != nil {
		log.Printf("UploadCourse: Error marshalling course data to JSON: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to prepare response data"})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "File processed successfully", // Changed message for clarity
		"data":    string(out),
	})
}

func UploadStudent(c *fiber.Ctx) error {
	db := database.DB.Db
	studentFile, err := c.FormFile("student") // Renamed variable for clarity
	if err != nil {
		log.Printf("UploadStudent: Error getting form file 'student': %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Error uploading student file"})
	}

	file, err := studentFile.Open()
	if err != nil {
		log.Printf("UploadStudent: Error opening uploaded file: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to open uploaded file"})
	}
	defer file.Close()

	f, err := excelize.OpenReader(file)
	if err != nil {
		log.Printf("UploadStudent: Error opening Excel reader: %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid Excel file format"})
	}

	rows, err := f.GetRows("Sheet1")
	if err != nil {
		log.Printf("UploadStudent: Error getting rows from Sheet1: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to process Excel sheet data"})
	}

	if len(rows) <= 1 { // Assuming first row is header
		log.Printf("UploadStudent: No data rows found in Sheet1 (or only header)")
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Excel sheet contains no student data or only a header"})
	}

	// N+1 Query Fix:
	// 1. Collect all unique department names from the Excel
	deptNames := make(map[string]struct{})
	for i, row := range rows {
		if i == 0 { continue } // Skip header row
		if len(row) > 3 { // Ensure department column exists
			deptNames[row[3]] = struct{}{}
		}
	}
	uniqueDeptNames := []string{}
	for name := range deptNames {
		uniqueDeptNames = append(uniqueDeptNames, name)
	}

	// 2. Fetch these departments in a single query
	var departments []models.Department
	if len(uniqueDeptNames) > 0 {
		if err := db.Where("name IN ?", uniqueDeptNames).Find(&departments).Error; err != nil {
			log.Printf("UploadStudent: Error fetching departments by names: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Database error while fetching department data"})
		}
	}
	deptMap := make(map[string]models.Department)
	for _, dept := range departments {
		deptMap[*dept.Name] = dept
	}

	notCreated := []string{}
	successfullyCreatedCount := 0

	// Iterate starting from the first data row (assuming row 0 is header)
	for i, row := range rows {
		if i == 0 { continue } // Skip header row
		if len(row) < 6 { // Ensure all expected columns are present
			log.Printf("UploadStudent: Skipping row %d due to insufficient columns: %v", i+1, row)
			notCreated = append(notCreated, fmt.Sprintf("Row %d (data: %s) - insufficient columns", i+1, row[0]))
			continue
		}

		department, ok := deptMap[row[3]]
		if !ok {
			log.Printf("UploadStudent: Department '%s' for USN '%s' not found in pre-fetched map. Skipping.", row[3], row[0])
			notCreated = append(notCreated, fmt.Sprintf("%s (department %s not found)", row[0], row[3]))
			continue
		}

		student := models.Student{
			Usn:              row[0],
			Name:             &row[1],
			Email:            &row[2],
			DepartmentID:     &department.ID, // Assign DepartmentID
			// Department:    department, // This would try to create/associate, not needed if only ID is stored
			PreviousCourse:   &row[4],
			PreviousCourseID: &row[5],
		}
		if err = db.Create(&student).Error; err != nil {
			log.Printf("UploadStudent: Error creating student with USN %s: %v", row[0], err)
			notCreated = append(notCreated, row[0])
		} else {
			successfullyCreatedCount++
		}
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": fmt.Sprintf("File processed. Successfully created %d students.", successfullyCreatedCount),
		"notCreated": notCreated, // Contains USNs or row identifiers of students not created
	})
}

func UploadData(c *fiber.Ctx) error {
	uploadedFile, err := c.FormFile("file")
	if err != nil {
		log.Printf("UploadData: Error getting form file 'file': %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Error uploading data file"})
	}

	// parseExcel already handles opening the file and reader
	studentData, courseData, err := parseExcel(uploadedFile)
	if err != nil {
		log.Printf("UploadData: Error parsing Excel file: %v", err)
		// parseExcel should return a more specific error if possible,
		// but for now, a general parsing error message is sent.
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("Failed to parse Excel file: %v", err)})
	}

	// The studentData from parseExcel is []string (students[0] in old code)
	// The courseData from parseExcel is []models.CourseData
	responseMap := map[string]interface{}{
		"students": studentData, // This was students[0] before, assuming it's a list of USNs
		"courses":  courseData,
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "Data file processed successfully",
		"data":    responseMap,
	})
}
func parseExcel(uploadedFile *multipart.FileHeader) ([]string, []models.CourseData, error) {
	file, err := uploadedFile.Open()
	if err != nil {
		log.Printf("parseExcel: Error opening uploaded file: %v", err)
		return nil, nil, fmt.Errorf("failed to open uploaded file: %w", err)
	}
	defer file.Close()

	f, err := excelize.OpenReader(file)
	if err != nil {
		log.Printf("parseExcel: Error opening Excel reader: %v", err)
		return nil, nil, fmt.Errorf("invalid Excel file format: %w", err)
	}

	// Get student USNs from Sheet1, Col1
	// Assuming the first column contains student USNs and the first row is a header.
	studentCols, err := f.GetCols("Sheet1")
	if err != nil {
		log.Printf("parseExcel: Error getting columns from Sheet1: %v", err)
		return nil, nil, fmt.Errorf("failed to read student data from Sheet1: %w", err)
	}
	var studentUSNs []string
	if len(studentCols) > 0 && len(studentCols[0]) > 1 { // Ensure there's at least one column and more than one row (for data)
		studentUSNs = studentCols[0][1:] // Skip header row studentCols[0][0]
	} else {
		log.Println("parseExcel: No student data found in Sheet1 or sheet is empty.")
		// Return empty slice instead of nil if no students, or handle as error depending on requirements
	}

	// Get course data from Sheet2
	// Assuming first row is header. Columns: Code, Name, Seats, Department
	coursesRow, err := f.GetRows("Sheet2")
	if err != nil {
		log.Printf("parseExcel: Error getting rows from Sheet2: %v", err)
		return nil, nil, fmt.Errorf("failed to read course data from Sheet2: %w", err)
	}

	var courses []models.CourseData
	// Iterate starting from the first data row (coursesRow[0] is header)
	for i, row := range coursesRow {
		if i == 0 { continue } // Skip header row
		if len(row) < 4 {
			log.Printf("parseExcel: Skipping course row %d in Sheet2 due to insufficient columns: %v", i+1, row)
			continue // Or return an error if strict parsing is needed
		}

		seats, err := strconv.ParseUint(row[2], 10, 32)
		if err != nil {
			log.Printf("parseExcel: Error parsing seats for course code %s (row %d): %v. Skipping.", row[0], i+1, err)
			continue // Or return an error
		}
		courseCode := row[0]
		courseName := row[1]
		departmentName := row[3]
		
		mapRow := models.CourseData{ // Assuming models.CourseData uses pointers for strings
			Code:       &courseCode,
			Name:       &courseName,
			Seats:      uint(seats),
			Department: &departmentName,
		}
		courses = append(courses, mapRow)
	}

	return studentUSNs, courses, nil
}
