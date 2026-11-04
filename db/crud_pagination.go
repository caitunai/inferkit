package db

// PagingRequest represents paging request data.
type PagingRequest struct {
	Cursor uint `form:"cursor" json:"cursor"`
	Limit  int  `form:"limit" json:"limit"`
	Prev   bool `form:"prev" json:"prev"` // true=find new data, false=find old data
}

// PagingResponse represents paging response data.
type PagingResponse[T any] struct {
	Data       []T  `json:"data"`
	NextCursor uint `json:"next_cursor"`
	PrevCursor uint `json:"prev_cursor"`
	HasNext    bool `json:"has_next"` // if it has next page
	HasPrev    bool `json:"has_prev"` // if it has prev page
}
