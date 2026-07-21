package httpserver

import "github.com/gin-gonic/gin"

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAPIError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": apiErrorBody{Code: code, Message: message}})
}
