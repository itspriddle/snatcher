// This file contains the domain types the Client decodes Prowlarr's REST
// responses into. Prowlarr's wire fields are camelCase; the domain types carry
// snake_case json tags, matching the Transmission domain model and the stable
// scripting surface snatcher's --json output exposes. Each type therefore has a
// small unexported wire struct that decodes the response and projects into it.
package prowlarr

// SystemStatus is a Prowlarr instance's self-description, returned by
// Client.SystemStatus. It models only the fields snatcher renders; the rest of
// Prowlarr's SystemResource is reachable through the raw api escape hatch.
type SystemStatus struct {
	// AppName is the application's own name, e.g. "Prowlarr".
	AppName string `json:"app_name"`

	// InstanceName is the operator-assigned name for this instance.
	InstanceName string `json:"instance_name"`

	// Version is the running version, e.g. "1.37.0.5076".
	Version string `json:"version"`
}

// systemStatusWire is Prowlarr's SystemResource as it arrives on the wire.
type systemStatusWire struct {
	AppName      string `json:"appName"`
	InstanceName string `json:"instanceName"`
	Version      string `json:"version"`
}

// status projects the wire shape into the domain type. The two differ only in
// their json tags, so the conversion is direct; the compiler enforces that they
// stay field-for-field identical, and a field that stops matching has to be
// projected explicitly here.
func (w systemStatusWire) status() SystemStatus {
	return SystemStatus(w)
}
