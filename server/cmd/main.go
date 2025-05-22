package main

import (
	"log"

	"github.com/AaronDennis07/electrum/internals/cache"
	"github.com/AaronDennis07/electrum/internals/database"
	"github.com/AaronDennis07/electrum/internals/handlers"
	"github.com/AaronDennis07/electrum/routers"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"

	"github.com/joho/godotenv"
)

func main() {

	err := godotenv.Load("./config/.env")

	if err != nil {
		log.Println("could not load config file ")
	}
	database.ConnectDB()
	cache.SetupCache()
	app := fiber.New()

	app.Use(cors.New(cors.Config{
		AllowOrigins:     os.Getenv("FRONTEND_URL"), //"http://localhost:5173",
		AllowCredentials: true,
	}))

	app.Use(logger.New())

	routers.SetupCourseRoutes(app)
	routers.SetupAuthRoutes(app)
	routers.SetupSessionhRoutes(app)
	routers.SetupStudentRoutes(app)
	log.Fatal(app.Listen(":8000"))
}
