package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/AaronDennis07/electrum/internals/database"
	"gorm.io/gorm"
	"github.com/AaronDennis07/electrum/internals/models"
	"github.com/gofiber/fiber/v2"
)

func CreateCourse(c *fiber.Ctx) error {
	db := database.DB.Db
	course := new(models.Course)
	err := c.BodyParser(course)

	if err != nil {
		log.Printf("Error parsing course body: %v", err)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	err = db.Create(&course).Error

	if err != nil {
		log.Printf("Error creating course: %v", err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create course",
		})
	}

	return c.Status(http.StatusCreated).JSON(fiber.Map{
		"data": course,
	})
}

func AllCourses(c *fiber.Ctx) error {
	var courses []models.Course
	db := database.DB.Db

	if err := db.Find(&courses).Error; err != nil {
		log.Printf("Error fetching all courses: %v", err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to retrieve courses",
		})
	}

	return c.JSON(fiber.Map{
		"data": courses,
	})
}

func GetCourse(c *fiber.Ctx) error {
	db := database.DB.Db
	var course models.Course
	id := c.Params("id")

	err := db.Where("id=?", id).First(&course).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.Status(http.StatusNotFound).JSON(fiber.Map{
				"error": "Course not found",
			})
		}
		log.Printf("Error fetching course by ID %s: %v", id, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to retrieve course",
		})
	}

	return c.JSON(fiber.Map{
		"data": course,
	})
}

func UpdateCourse(c *fiber.Ctx) error {
	db := database.DB.Db
	type UpdateCourse struct {
		Name string
		Code string
	}

	var course models.Course

	id := c.Params("id")

	err := db.Where("id=?", id).First(&course).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.Status(http.StatusNotFound).JSON(fiber.Map{
				"error": "Course not found",
			})
		}
		log.Printf("Error fetching course for update ID %s: %v", id, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to retrieve course for update",
		})
	}

	var updatedCourse UpdateCourse

	err = c.BodyParser(&updatedCourse)

	if err != nil {
		log.Printf("Error parsing update course body for ID %s: %v", id, err)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	course.Code = &updatedCourse.Code
	course.Name = &updatedCourse.Name

	if err := db.Save(&course).Error; err != nil {
		log.Printf("Error updating course ID %s: %v", id, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update course",
		})
	}

	return c.JSON(fiber.Map{
		"data": course,
	})
}

func DeleteCourse(c *fiber.Ctx) error {
	db := database.DB.Db
	id := c.Params("id")
	var course models.Course

	err := db.Where("id=?", id).First(&course).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.Status(http.StatusNotFound).JSON(fiber.Map{
				"error": "Course not found",
			})
		}
		log.Printf("Error fetching course for delete ID %s: %v", id, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to retrieve course for deletion",
		})
	}

	err = db.Delete(&course).Error

	if err != nil {
		log.Printf("Error deleting course ID %s: %v", id, err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to delete course",
		})
	}
	return c.JSON(fiber.Map{
		"message": "Course deleted successfully",
	})
}
