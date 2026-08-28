package release

type Release struct {
	ID        int64
	Artifacts []Artifact
}

type Artifact struct {
	Name    string `json:"name"`
	Expired bool   `json:"expired"`
}
