package svc

// Svc contains the service information.
type Svc struct {
	// Version is the service version.
	Version string

	// Name is the name of the service.
	Name string
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

// GetServiceInfo returns the complete service name.
func (s Svc) GetServiceInfo() string {
	return s.Name + ":" + s.Version
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

// Info returns the service information.
func Info() Svc {
	return svc
}
