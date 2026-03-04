package ports

// MediaMeta describes a stored media object.
// Keep this struct minimal so agent core does not depend on infra packages.
type MediaMeta struct {
	Filename    string
	ContentType string
	Source      string
}

// MediaResolver resolves media refs (e.g. "media://...") into local file paths.
type MediaResolver interface {
	ResolveWithMeta(ref string) (localPath string, meta MediaMeta, err error)
}
