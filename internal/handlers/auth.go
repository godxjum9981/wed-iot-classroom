package handlers

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/classroom/go-example/internal/config"
	"github.com/classroom/go-example/internal/db"
	"github.com/classroom/go-example/internal/middleware"
	"github.com/classroom/go-example/internal/models"
	"log"
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// POST /api/auth/login
func Login(c fiber.Ctx) error {
	var req loginReq
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Username == "" || req.Password == "" {
		return c.Status(400).JSON(fiber.Map{"error": "username and password required"})
	}

	var user models.User
	err := db.DB.SelectOne(&user,
    `SELECT * FROM users WHERE username = $1`, req.Username)
if err != nil {
    log.Printf("[Login] user not found: %v | username: %s", err, req.Username)
    return c.Status(401).JSON(fiber.Map{"error": "invalid credentials"})
}

log.Printf("[Login] found user: %s | hash: %s", user.Username, user.PasswordHash)

if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
    log.Printf("[Login] password mismatch for: %s", user.Username)
    return c.Status(401).JSON(fiber.Map{"error": "invalid credentials"})
}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		return c.Status(401).JSON(fiber.Map{"error": "invalid credentials"})
	}

	// Update last_login
	now := time.Now()
	user.LastLogin = &now
	db.DB.Exec(`UPDATE users SET last_login=$1 WHERE id=$2`, now, user.ID)

	token, err := generateJWT(user.ID, user.Username)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "token generation failed"})
	}

	return c.JSON(fiber.Map{
		"token":    token,
		"user_id":  user.ID,
		"username": user.Username,
	})
}

// POST /api/auth/register
func Register(c fiber.Ctx) error {
	var req loginReq
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	if len(req.Password) < 6 {
		return c.Status(400).JSON(fiber.Map{"error": "password must be at least 6 characters"})
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "hashing failed"})
	}

	user := models.User{
		ID:           uuid.New().String(),
		Username:     req.Username,
		PasswordHash: string(hash),
		CreatedAt:    time.Now(),
	}

	if err := db.DB.Insert(&user); err != nil {
		return c.Status(409).JSON(fiber.Map{"error": "username already taken"})
	}

	return c.Status(201).JSON(fiber.Map{"message": "user created", "user_id": user.ID})
}

// GET /api/auth/me
func Me(c fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var user models.User
	if err := db.DB.SelectOne(&user, `SELECT * FROM users WHERE id=$1`, userID); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}
	return c.JSON(user)
}

func generateJWT(userID, username string) (string, error) {
	claims := middleware.Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.C.JWTSecret))
}