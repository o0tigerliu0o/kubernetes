package diskaffinity

import (
	"errors"
	v1 "k8s.io/api/core/v1"
	"kubernetes/pkg/controller/volume/scheduling"
	framework "kubernetes/pkg/scheduler/framework/v1alpha1"
	"sync"
)

const (
	// DefaultBindTimeoutSeconds defines the default bind timeout in seconds
	DefaultBindTimeoutSeconds = 600

	stateKey framework.StateKey = Name
)

// the state is initialized in PreFilter phase. because we save the pointer in
// framework.CycleState, in the later phases we don't need to call Write method
// to update the value
type stateData struct {
	skip         bool // set true if pod does not have PVCs
	boundClaims  []*v1.PersistentVolumeClaim
	claimsToBind []*v1.PersistentVolumeClaim
	allBound     bool
	// podVolumesByNode holds the pod's volume information found in the Filter
	// phase for each node
	// it's initialized in the PreFilter phase
	podVolumesByNode map[string]*scheduling.PodVolumes
	sync.Mutex
}

func (d *stateData) Clone() framework.StateData {
	return d
}



func getStateData(cs *framework.CycleState) (*stateData, error) {
	state, err := cs.Read(stateKey)
	if err != nil {
		return nil, err
	}
	s, ok := state.(*stateData)
	if !ok {
		return nil, errors.New("unable to convert state into stateData")
	}
	return s, nil
}
