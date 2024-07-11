package release

type Release struct {
	Id   string `json:"Id"`
	Date string `json:"Date"`
	Arch string `json:"Arch"`
	Url  string `json:"Url"`
}
