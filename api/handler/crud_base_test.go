package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/db"
)

func TestCrudControllerRejectsInvalidIDWithSentinelMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controller := &CrudController[
		*db.User,
		*UserCreateForm,
		*UserUpdateForm,
		*UserPublicView,
		*UserSearchInput,
	]{}
	tests := []struct {
		handle func(*base.Context)
		method string
		name   string
	}{
		{name: "get", method: http.MethodGet, handle: controller.Get},
		{name: "update", method: http.MethodPut, handle: controller.Update},
		{name: "delete", method: http.MethodDelete, handle: controller.Delete},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(response)
			ginContext.Params = gin.Params{{Key: "id", Value: "invalid"}}
			ginContext.Request = httptest.NewRequestWithContext(t.Context(), test.method, "/invalid", nil)
			ginContext.Request.Header.Set("Accept", "application/json")

			test.handle(&base.Context{Context: ginContext})

			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
			}
			if !strings.Contains(response.Body.String(), strconv.Quote(ErrInvalidID.Error())) {
				t.Fatalf("response = %s, want message %q", response.Body.String(), ErrInvalidID.Error())
			}
		})
	}
}
