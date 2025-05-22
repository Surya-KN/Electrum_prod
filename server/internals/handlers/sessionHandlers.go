package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/AaronDennis07/electrum/internals/cache"
	"github.com/AaronDennis07/electrum/internals/ctx"
	"github.com/AaronDennis07/electrum/internals/database"
	"github.com/AaronDennis07/electrum/internals/models"
	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

type Session struct {
	Courses  map[string]string
	Students map[string]string
}
type SessionRequest struct {
	Session models.Session `json:"session"`
	// Courses  []CourseRequest `json:"courses"`
	// Students []string        `json:"students"`
}
type CourseRequest struct {
	Name       string `json:"name"`
	Code       string `json:"code"`
	Department string `json:"department"`
}

func CreateSession(c *fiber.Ctx) error {
	db := database.DB.Db

	uploadedFile, err := c.FormFile("file")
	if err != nil {
		log.Printf("Error uploading file for session creation: %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "File upload failed",
		})
	}
	students, CourseData, err := parseExcel(uploadedFile)
	if err != nil {
		log.Printf("Error parsing excel file for session creation: %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Excel parsing failed",
		})
	}

	request := new(SessionRequest)
	re := c.FormValue("data")
	if err = json.Unmarshal([]byte(re), &request); err != nil {
		log.Printf("Error unmarshalling session data: %v", err)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid session data format",
		})
	}

	// TODO: Wrap the following DB operations in a transaction

	//adding courses
	for _, reqCourse := range CourseData {
		var department models.Department
		// Consider fetching all departments in one go before this loop to avoid N+1
		if err := db.Where("name=?", reqCourse.Department).First(&department).Error; err != nil {
			log.Printf("Error finding department %s: %v", reqCourse.Department, err)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("Department %s not found", reqCourse.Department)})
			}
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error while finding department"})
		}
		course := models.Course{
			Name:         reqCourse.Name,
			Code:         reqCourse.Code,
			Seats:        &reqCourse.Seats,
			DepartmentID: &department.ID, // Assign DepartmentID directly
		}
		if err = db.Create(&course).Error; err != nil {
			log.Printf("Error creating course %s (%s): %v", *course.Name, *course.Code, err)
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create course in database"})
		}
		request.Session.Courses = append(request.Session.Courses, course)
	}

	//creating session
	status := "upcoming"
	request.Session.Status = &status
	if err = db.Create(&request.Session).Error; err != nil {
		log.Printf("Error creating session %s: %v", *request.Session.Name, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create session in database"})
	}

	//checking if students exist
	// Consider fetching all students in one go before this loop to avoid N+1
	var notFound []string = []string{}
	for _, usn := range students {
		var student models.Student
		if err = db.Where("usn=?", usn).First(&student).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				notFound = append(notFound, usn)
				log.Printf("Student with USN %s not found, skipping enrollment creation.", usn)
				continue
			}
			log.Printf("Error finding student with USN %s: %v", usn, err)
			// Decide if this should be a critical error or just skip the student
			// For now, skipping.
			continue
		}
		enrollment := models.Enrollment{
			StudentID: &usn,
			SessionID: &request.Session.ID,
		}
		if err = db.Create(&enrollment).Error; err != nil {
			log.Printf("Error creating enrollment for student %s in session %d: %v", usn, request.Session.ID, err)
			// Decide if this should be a critical error. For now, logging and continuing.
		}
	}
	var count int64
	if err := db.Model(&models.Enrollment{}).Where("session_id=?", request.Session.ID).Count(&count).Error; err != nil {
		log.Printf("Error counting enrollments for session %d: %v", request.Session.ID, err)
		// Non-critical, but session total_students might be inaccurate
	}

	if err = db.Model(&request.Session).Update("total_students", count).Error; err != nil {
		log.Printf("Error updating total_students for session %d: %v", request.Session.ID, err)
	}
	if err = db.Model(&request.Session).Update("applied_students", 0).Error; err != nil {
		log.Printf("Error updating applied_students for session %d: %v", request.Session.ID, err)
	}

	var createdSession models.Session
	if err = db.Preload("Courses").Find(&createdSession, request.Session.ID).Error; err != nil {
		log.Printf("Error reloading session %d with courses: %v", request.Session.ID, err)
		// May not be critical for response if session object is already mostly populated.
	}
	var enrolledStudents []models.Enrollment
	if err = db.Preload("Student").Where("session_id=?", request.Session.ID).Find(&enrolledStudents).Error; err != nil {
		log.Printf("Error loading enrollments with student details for session %d: %v", request.Session.ID, err)
		// May not be critical for response if notFound list is sufficient.
	}
	return c.Status(http.StatusCreated).JSON(fiber.Map{
		"session":  createdSession,
		"enrolled": enrolledStudents,
		"notFound": notFound,
	})
}

func StartSession(c *fiber.Ctx) error {

	db := database.DB.Db
	// var session models.Session
	// db.First(&session, c.Params("session"))
	// if session.ID == 0 {
	// 	return c.Status(http.StatusBadRequest).JSON(fiber.Map{
	// 		"message": "Session does not exist",
	// 	})
	// }

	sessionName := c.Params("session")

	var sessionDb models.Session
	if err := db.Preload("Courses").Where("name=?", sessionName).First(&sessionDb).Error; err != nil {
		log.Printf("Error finding session %s to start: %v", sessionName, err)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Session not found"})
		}
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error while finding session"})
	}

	if err := db.Model(&sessionDb).Update("status", "open").Error; err != nil {
		log.Printf("Error updating session %s status to open: %v", sessionName, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update session status"})
	}

	courseKey := sessionName + ":courses"
	studentKey := sessionName + ":students"

	if sessionExists(courseKey, studentKey) {
		log.Printf("Attempted to start session %s which already exists in Redis.", sessionName)
		return c.Status(http.StatusConflict).JSON(fiber.Map{"error": "Session " + sessionName + " is already active"})
	}

	for _, course := range sessionDb.Courses {
		if course.Code == nil || course.Seats == nil {
			log.Printf("Skipping course with nil code or seats during session %s start. Course ID: %d", sessionName, course.ID)
			continue
		}
		if err := cache.Client.Redis.HSet(ctx.Ctx, courseKey, *course.Code, *course.Seats).Err(); err != nil {
			log.Printf("Error populating Redis for course %s in session %s: %v", *course.Code, sessionName, err)
			// Decide if this is a critical error. For now, logging and continuing.
		}
	}

	var enrollments []models.Enrollment
	if err := db.Preload("Student").Preload("Course1").Where("Session_ID = ?", sessionDb.ID).Find(&enrollments).Error; err != nil {
		log.Printf("Error fetching enrollments for session %s: %v", sessionName, err)
		// Decide if this is critical. For now, proceeding with potentially empty/partial enrollments in Redis.
	}

	for _, enrollment := range enrollments {
		if enrollment.StudentID == nil {
			log.Printf("Skipping enrollment with nil StudentID during session %s start. Enrollment ID: %d", sessionName, enrollment.ID)
			continue
		}
		studentCourseCode := ""
		if enrollment.Course1ID != nil && enrollment.Course1 != nil && enrollment.Course1.Code != nil {
			studentCourseCode = *enrollment.Course1.Code
			// Decrement course seats in Redis
			if err := cache.Client.Redis.HIncrBy(ctx.Ctx, courseKey, studentCourseCode, -1).Err(); err != nil {
				log.Printf("Error decrementing Redis seats for course %s in session %s: %v", studentCourseCode, sessionName, err)
			}
		}
		if err := cache.Client.Redis.HSet(ctx.Ctx, studentKey, *enrollment.StudentID, studentCourseCode).Err(); err != nil {
			log.Printf("Error populating Redis for student %s in session %s: %v", *enrollment.StudentID, sessionName, err)
		}
	}

	return c.Status(http.StatusOK).JSON(fiber.Map{
		"message":  "Session " + sessionName + " started successfully",
		"courses":  cache.Client.Redis.HGetAll(ctx.Ctx, courseKey).Val(),
		"students": cache.Client.Redis.HGetAll(ctx.Ctx, studentKey).Val(),
	})
}

func SubscribeToSession(c *websocket.Conn) {

	channel := c.Params("session")
	courseKey := channel + ":courses"
	pubsub := cache.Client.Redis.Subscribe(ctx.Ctx, channel)
	defer pubsub.Close() // Ensure pubsub is closed when the handler exits

	ch := pubsub.Channel()

	// Send initial course data
	courses := cache.Client.Redis.HGetAll(ctx.Ctx, courseKey).Val()
	if len(courses) != 0 {
		jsonCourses, err := json.Marshal(courses)
		if err != nil {
			log.Printf("SubscribeToSession (%s): Error marshalling initial courses: %v", channel, err)
			// Consider sending a websocket close message or an error message
			// c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Error processing data"))
			return // Terminate connection or handler on critical error
		}
		if err := c.WriteMessage(websocket.TextMessage, jsonCourses); err != nil {
			log.Printf("SubscribeToSession (%s): Error sending initial courses: %v", channel, err)
			return // Client disconnected or error writing
		}
	}

	// Listen for and forward messages from Redis channel
	for msg := range ch {
		log.Printf("SubscribeToSession (%s): Received payload: %s", channel, msg.Payload)

		// Assuming payload is already a JSON map as per EnrollToCourse publisher
		// No need to unmarshal and marshal again if it's already a map string
		// If it's a string representation of a JSON object, then unmarshalling is needed.
		// For now, let's assume msg.Payload is the JSON string to be sent.
		
		// Validate if msg.Payload is valid JSON before sending (optional, but good practice)
		var tempJson json.RawMessage
		if err := json.Unmarshal([]byte(msg.Payload), &tempJson); err != nil {
			log.Printf("SubscribeToSession (%s): Received invalid JSON payload: %v. Payload: %s", channel, err, msg.Payload)
			continue // Skip sending invalid JSON
		}


		if err := c.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
			log.Printf("SubscribeToSession (%s): Error writing message: %v", channel, err)
			return // Client disconnected or error writing, terminate handler
		}
		log.Printf("SubscribeToSession (%s): Successfully sent message to client.", channel)
	}
	log.Printf("SubscribeToSession (%s): Channel closed, handler exiting.", channel)
}

func sessionExists(key1 string, key2 string) bool {
	return cache.Client.Redis.Exists(ctx.Ctx, key1, key2).Val() > 0
}

func EnrollToCourse(c *fiber.Ctx) error {
	channel := c.Params("session")
	courseKey := channel + ":courses"
	studentKey := channel + ":students"
	req := struct {
		ID     string
		Course string
	}{}
	if err := c.BodyParser(&req); err != nil {
		log.Printf("Error parsing enroll request for session %s: %v", channel, err)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request data"})
	}

	if !sessionExists(courseKey, studentKey) {
		log.Printf("Attempt to enroll in non-existent or inactive session %s", channel)
		return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Session is not active or does not exist"})
	}

	courseSeatsStr, err := cache.Client.Redis.HGet(ctx.Ctx, courseKey, req.Course).Result()
	if err == redis.Nil {
		log.Printf("Attempt to enroll in non-existent course %s in session %s", req.Course, channel)
		return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Course does not exist in this session"})
	} else if err != nil {
		log.Printf("Redis error getting course %s for session %s: %v", req.Course, channel, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Could not retrieve course details"})
	}

	studentCurrentCourse, err := cache.Client.Redis.HGet(ctx.Ctx, studentKey, req.ID).Result()
	if err == redis.Nil {
		log.Printf("Student %s not found in enrollment list for session %s", req.ID, channel)
		return c.Status(http.StatusForbidden).JSON(fiber.Map{"error": "Student is not eligible for this session"})
	} else if err != nil {
		log.Printf("Redis error getting student %s for session %s: %v", req.ID, channel, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Could not retrieve student enrollment status"})
	}

	if studentCurrentCourse != "" {
		log.Printf("Student %s attempted to enroll in course %s but already enrolled in %s for session %s", req.ID, req.Course, studentCurrentCourse, channel)
		return c.Status(http.StatusConflict).JSON(fiber.Map{"error": "Student already enrolled in course: " + studentCurrentCourse})
	}

	courseSeatsInt, err := strconv.Atoi(courseSeatsStr)
	if err != nil {
		log.Printf("Error converting course seats %s to int for course %s session %s: %v", courseSeatsStr, req.Course, channel, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Invalid course seat data"})
	}

	if courseSeatsInt <= 0 {
		log.Printf("Course %s in session %s has no available seats.", req.Course, channel)
		return c.Status(http.StatusConflict).JSON(fiber.Map{"error": "Course is full"})
	}

	// Pipeline Redis commands for atomicity (though individual HSet/HIncrBy are atomic)
	pipe := cache.Client.Redis.Pipeline()
	pipe.HSet(ctx.Ctx, studentKey, req.ID, req.Course)
	pipe.HIncrBy(ctx.Ctx, courseKey, req.Course, -1)
	_, err = pipe.Exec(ctx.Ctx)
	if err != nil {
		log.Printf("Redis pipeline error during enrollment for student %s, course %s, session %s: %v", req.ID, req.Course, channel, err)
		// Attempt to rollback HSet if HIncrBy failed? Or rely on eventual consistency/manual check.
		// For now, returning server error.
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Enrollment update failed"})
	}


	courses := cache.Client.Redis.HGetAll(ctx.Ctx, courseKey).Val() // Get updated course list

	go func(studentID, courseCode, sessionName string) {
		db := database.DB.Db
		var session models.Session
		if err := db.Where("name=?", sessionName).First(&session).Error; err != nil {
			log.Printf("Goroutine: Error finding session %s: %v", sessionName, err)
			return
		}
		var courseModel models.Course // Renamed to avoid conflict with outer 'course' variable
		if err := db.Where("code=? AND session_id=?", courseCode, session.ID).First(&courseModel).Error; err != nil {
			log.Printf("Goroutine: Error finding course %s for session %d: %v", courseCode, session.ID, err)
			return
		}

		// Use transaction for DB updates
		tx := db.Begin()
		if err := tx.Model(&courseModel).Update("seats_filled", gorm.Expr("seats_filled + ?", 1)).Error; err != nil {
			tx.Rollback()
			log.Printf("Goroutine: Error updating seats_filled for course %s: %v", courseCode, err)
			return
		}
		if err := tx.Model(&session).Update("applied_students", gorm.Expr("applied_students + ?", 1)).Error; err != nil {
			tx.Rollback()
			log.Printf("Goroutine: Error updating applied_students for session %s: %v", sessionName, err)
			return
		}
		if err := tx.Model(&models.Enrollment{}).Where("session_id=? AND student_id=?", session.ID, studentID).Update("course1_id", courseModel.ID).Error; err != nil {
			tx.Rollback()
			log.Printf("Goroutine: Error updating enrollment for student %s, course %s: %v", studentID, courseCode, err)
			return
		}
		if err := tx.Commit().Error; err != nil {
			log.Printf("Goroutine: Error committing transaction for student %s, course %s: %v", studentID, courseCode, err)
		}
	}(req.ID, req.Course, channel)

	jsonCourses, err := json.Marshal(courses)
	if err != nil {
		log.Printf("Error marshalling courses to JSON for publishing in session %s: %v", channel, err)
		// Don't publish if marshalling fails, but enrollment itself was successful.
	} else {
		if err := cache.Client.Redis.Publish(ctx.Ctx, channel, string(jsonCourses)).Err(); err != nil {
			log.Printf("Error publishing course update to Redis channel %s: %v", channel, err)
		}
	}

	return c.Status(http.StatusOK).JSON(fiber.Map{"message": "Successfully enrolled"})
}

func StopSession(c *fiber.Ctx) error {
	db := database.DB.Db
	sessionName := c.Params("session")

	// Attempt to delete Redis keys first
	deletedKeysCount := cache.Client.Redis.Del(ctx.Ctx, sessionName+":courses", sessionName+":students").Val()

	if deletedKeysCount == 0 {
		log.Printf("No active session found in Redis for %s to stop.", sessionName)
		// Even if not in Redis, try to mark it as closed in DB if it exists there
	}

	var session models.Session
	// It's important to find the session first to ensure it exists before updating.
	if err := db.Where("name = ?", sessionName).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("Session %s not found in database to mark as closed.", sessionName)
			// If it wasn't in Redis either, then it truly doesn't exist or isn't active.
			if deletedKeysCount == 0 {
				return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Session not found or not active"})
			}
			// If it was in Redis but not DB, this is an inconsistency. Log and proceed.
			log.Printf("Inconsistency: Session %s was in Redis but not in DB.", sessionName)
		} else {
			log.Printf("Database error finding session %s to stop: %v", sessionName, err)
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error while finding session"})
		}
	}

	// If session was found in DB, update its status
	if session.ID != 0 {
		if err := db.Model(&session).Update("status", "closed").Error; err != nil {
			log.Printf("Failed to update session %s status to closed: %v", sessionName, err)
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update session status in database"})
		}
	} else if deletedKeysCount > 0 {
		// This case means keys were deleted from Redis, but the session was not found in DB.
		// This is an inconsistency, but the primary goal (stopping activity in Redis) is achieved.
		log.Printf("Session %s was active in Redis but not found in DB. Marked as stopped by Redis key deletion.", sessionName)
		return c.Status(http.StatusOK).JSON(fiber.Map{"message": "Session activity stopped (was active in Redis, not found in DB)"})
	}


	if deletedKeysCount == 0 && session.ID == 0 {
		// This means it wasn't in Redis and wasn't found in DB.
		return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Session not found or not active"})
	}
	
	log.Printf("Session %s stopped successfully (Redis keys deleted: %d, DB status updated).", sessionName, deletedKeysCount)
	return c.Status(http.StatusOK).JSON(fiber.Map{"message": "Session stopped successfully"})
}

type CourseData struct {
	Id         uint
	Name       string
	Code       string
	Seats      uint
	Department string
}

func GetSession(c *fiber.Ctx) error {
	db := database.DB.Db
	sessionName := c.Params("session")
	var session models.Session

	if err := db.Where("name=?", sessionName).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("GetSession: Session %s not found.", sessionName)
			return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Session not found"})
		}
		log.Printf("GetSession: Database error finding session %s: %v", sessionName, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error retrieving session"})
	}

	sessionId := session.ID
	var courses []models.Course
	if err := db.Preload("Department").Where("session_id=?", sessionId).Find(&courses).Error; err != nil {
		log.Printf("GetSession: Database error finding courses for session ID %d (%s): %v", sessionId, sessionName, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error retrieving courses for session"})
	}

	courseData := []CourseData{}
	for _, course := range courses {
		// Add checks for nil pointers if any of these can be nil in the DB
		// and the response structure requires non-nil values.
		// For now, assuming they are expected to be populated if the course exists.
		if course.Name == nil || course.Code == nil || course.Seats == nil || course.Department.Name == nil {
			log.Printf("GetSession: Course with ID %d in session %s has missing essential data, skipping.", course.ID, sessionName)
			continue
		}
		courseData = append(courseData, CourseData{
			Id:         course.ID,
			Name:       *course.Name,
			Code:       *course.Code,
			Seats:      *course.Seats,
			Department: *course.Department.Name,
		})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{
		"courses": courseData,
	})
}
func GetAllSessions(c *fiber.Ctx) error {
	db := database.DB.Db
	var sessions []models.Session
	if err := db.Preload("Courses").Find(&sessions).Error; err != nil {
		log.Printf("GetAllSessions: Database error fetching all sessions: %v", err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to retrieve sessions",
		})
	}
	return c.Status(http.StatusOK).JSON(sessions)
}

func GetSessionDetails(c *fiber.Ctx) error {
	db := database.DB.Db
	sessionName := c.Params("session")
	var session models.Session

	if err := db.Preload("Courses").Where("name=?", sessionName).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("GetSessionDetails: Session %s not found.", sessionName)
			return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Session not found"})
		}
		log.Printf("GetSessionDetails: Database error finding session %s: %v", sessionName, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error retrieving session details"})
	}

	// TODO: Decide if enrollments should be part of this response.
	// If so, they should be loaded here with proper error handling.
	// For now, only session details (including its courses) are returned.

	return c.Status(http.StatusOK).JSON(fiber.Map{
		"session": session,
	})

}
func SendEnrollmentsExcel(c *fiber.Ctx) error {
	f := excelize.NewFile()
	index, _ := f.NewSheet("Sheet1")
	f.SetActiveSheet(index)

	// Set the titles for the columns
	f.SetCellValue("Sheet1", "A1", "Student ID")
	f.SetCellValue("Sheet1", "B1", "Student Name")
	f.SetCellValue("Sheet1", "C1", "Course Name")
	f.SetCellValue("Sheet1", "D1", "Course Code")

	// Retrieve enrollments from the database
	db := database.DB.Db
	sessionName := c.Params("session")
	var session models.Session

	if err := db.Where("name=?", sessionName).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("SendEnrollmentsExcel: Session %s not found.", sessionName)
			return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Session not found"})
		}
		log.Printf("SendEnrollmentsExcel: Database error finding session %s: %v", sessionName, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error retrieving session"})
	}

	if session.ID == 0 { // Should be covered by the check above, but as a safeguard.
		log.Printf("SendEnrollmentsExcel: Session %s has ID 0 after query.", sessionName)
		return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Session not found (ID 0)"})
	}

	var enrollments []models.Enrollment
	if err := db.Preload("Course1").Preload("Student").Where("session_id=?", session.ID).Find(&enrollments).Error; err != nil {
		log.Printf("SendEnrollmentsExcel: Database error fetching enrollments for session ID %d (%s): %v", session.ID, sessionName, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error retrieving enrollments"})
	}

	// Populate the Excel file
	for i, enrollment := range enrollments {
		// Check for nil pointers on student and course, and their fields, before dereferencing
		studentID := ""
		if enrollment.StudentID != nil {
			studentID = *enrollment.StudentID
		}
		studentName := ""
		if enrollment.Student != nil && enrollment.Student.Name != nil {
			studentName = *enrollment.Student.Name
		}
		courseName := ""
		courseCode := ""
		if enrollment.Course1 != nil {
			if enrollment.Course1.Name != nil {
				courseName = *enrollment.Course1.Name
			}
			if enrollment.Course1.Code != nil {
				courseCode = *enrollment.Course1.Code
			}
		}

		row := i + 2 // Start from the second row, considering the header
		f.SetCellValue("Sheet1", "A"+strconv.Itoa(row), studentID)
		f.SetCellValue("Sheet1", "B"+strconv.Itoa(row), studentName)
		f.SetCellValue("Sheet1", "C"+strconv.Itoa(row), courseName)
		f.SetCellValue("Sheet1", "D"+strconv.Itoa(row), courseCode)
	}

	buffer, err := f.WriteToBuffer()
	if err != nil {
		log.Printf("SendEnrollmentsExcel: Failed to write Excel to buffer for session %s: %v", sessionName, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate Excel file",
		})
	}
	// No need to reassign to another buf variable, buffer.Bytes() can be used directly or buffer itself if Send expects an io.Reader
	// Set the appropriate headers for file download
	c.Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Set("Content-Disposition", "attachment; filename=\"enrollments.xlsx\"")

	// Send the file
	return c.Send(buffer.Bytes())
}
func safeDerefString(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

func CheckEnrollment(c *fiber.Ctx) error {
	db := database.DB.Db
	sessionName := c.Params("session")
	studentUSN := c.Params("student")

	var session models.Session
	if err := db.Where("name=?", sessionName).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("CheckEnrollment: Session %s not found.", sessionName)
			return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Session not found"})
		}
		log.Printf("CheckEnrollment: Database error finding session %s: %v", sessionName, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error retrieving session"})
	}

	// No need to query student separately if we only need the USN for the enrollment query.
	// If student details were needed, then a separate query with error handling for student not found would be here.
	// var student models.Student
	// if err := db.Where("usn=?", studentUSN).First(&student).Error; err != nil {
	// 	if errors.Is(err, gorm.ErrRecordNotFound) {
	// 		log.Printf("CheckEnrollment: Student %s not found.", studentUSN)
	// 		return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Student not found"})
	// 	}
	// 	log.Printf("CheckEnrollment: Database error finding student %s: %v", studentUSN, err)
	// 	return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error retrieving student"})
	// }

	var enrollment models.Enrollment
	// Assuming studentUSN is the actual student_id to be used in enrollments table.
	err := db.Preload("Course1").Where("session_id=? AND student_id=?", session.ID, studentUSN).First(&enrollment).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("CheckEnrollment: Student %s not enrolled in session %s.", studentUSN, sessionName)
			return c.Status(http.StatusOK).JSON(fiber.Map{ // OK status, but enrolled: false
				"message":  "Student is not enrolled in any course for this session.",
				"enrolled": false,
			})
		}
		log.Printf("CheckEnrollment: Database error finding enrollment for student %s in session %s: %v", studentUSN, sessionName, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Database error retrieving enrollment status"})
	}

	// Check if Course1ID is nil or if Course1 itself or Course1.Code is nil
	courseCode := ""
	if enrollment.Course1ID != nil && enrollment.Course1 != nil && enrollment.Course1.Code != nil {
		courseCode = *enrollment.Course1.Code
	} else {
		// This case implies an enrollment record exists but is incomplete or points to a deleted/invalid course.
		log.Printf("CheckEnrollment: Student %s is enrolled in session %s, but course details are missing. EnrollmentID: %d", studentUSN, sessionName, enrollment.ID)
		return c.Status(http.StatusOK).JSON(fiber.Map{ // Or potentially an internal server error depending on data integrity expectations
			"message":  "Student is enrolled, but course details are unavailable.",
			"enrolled": true, // Enrolled in session, but course specific data is missing.
			"coursecode": nil, // Explicitly nil
		})
	}

	return c.Status(http.StatusOK).JSON(fiber.Map{
		"message":    "Student is enrolled in the session.",
		"coursecode": courseCode,
		"enrolled":   true,
	})

}
