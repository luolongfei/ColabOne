//go:build !windows

package vault

func colabOneReadLegacySecret(service, key string) ([]byte, bool, error) {
	return nil, false, nil
}

func colabOneDeleteLegacySecret(service, key string) error {
	return nil
}
