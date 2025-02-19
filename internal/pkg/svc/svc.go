package svc

// Svc contains the service information.
type Svc struct {
	// Version is the service version.
	Version string

	// Name is the name of the service.
	Name string

	// AuthPrivateKeyPath is the path to the private key.
	AuthPrivateKeyPath string

	// AuthPublicKeyPath is the path to the public key.
	AuthPublicKeyPath string
}

// Svc represents the service.
var svc Svc

// GetVersion returns the service version.
func (s Svc) GetVersion() string {
	return s.Version
}

// GetName returns the service name.
func (s Svc) GetName() string {
	return s.Name
}

// GetAuthPrivateKeyPath returns the path to the private key.
func (s Svc) GetAuthPrivateKeyPath() string {
	return s.AuthPrivateKeyPath
}

// GetAuthPublicKeyPath returns the path to the public key.
func (s Svc) GetAuthPublicKeyPath() string {
	return s.AuthPublicKeyPath
}

// SetVersion sets the service version.
func SetVersion(version string) {
	if svc.Version != "" {
		return
	}
	svc.Version = version
}

// SetName sets the service name.
func SetName(name string) {
	if svc.Name != "" {
		return
	}
	svc.Name = name
}

// SetAuthPrivateKeyPath sets the path to the private key.
func SetAuthPrivateKeyPath(path string) {
	if svc.AuthPrivateKeyPath != "" {
		return
	}
	svc.AuthPrivateKeyPath = path
}

// SetAuthPublicKeyPath sets the path to the public key.
func SetAuthPublicKeyPath(path string) {
	if svc.AuthPublicKeyPath != "" {
		return
	}
	svc.AuthPublicKeyPath = path
}

// Info returns the service information.
func Info() Svc {
	return svc
}
