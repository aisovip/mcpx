//go:build !windows && !darwin

package browseruse

func DiscoverInstalledOfficial() ([]Installation, error) {
	return nil, ErrUnsupported
}
