package git

type Provider interface {
	Name() string
	Remote() string
}
