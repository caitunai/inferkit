package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/db"
)

// HomePage performs the home page operation.
func HomePage(c *base.Context) {
	var user *db.User
	if c.IsDatabaseEnabled() {
		user = c.LoginUser()
	}
	if user == nil {
		c.Forbidden("you are not login", gin.H{
			"db_enabled": c.IsDatabaseEnabled(),
		})
	} else {
		c.Success(gin.H{
			"data": fmt.Sprintf("your user id is: %d", user.ID),
		})
	}
}

// APIHomePage performs the api home page operation.
func APIHomePage(c *base.Context) {
	user := c.GetAPIUser()
	if user == nil {
		c.Forbidden("you are not login", gin.H{})
	}
	c.Success(gin.H{
		"user": user,
	})
}
