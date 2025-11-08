package onenote

type NotebooksResponse struct {
	Value []Notebook `json:"value"`
}

type Notebook struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type SectionsResponse struct {
	Value []Section `json:"value"`
}

type Section struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type PagesResponse struct {
	Value []Page `json:"value"`
}

type Page struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}
