package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"sync"

	backendInit "github.com/hashicorp/terraform/backend/init"
	"github.com/hashicorp/terraform/configs/hcl2shim"
	"github.com/lyraproj/hiera/hieraapi"
	"github.com/lyraproj/issue/issue"
	"github.com/lyraproj/pcore/px"
	"github.com/zclconf/go-cty/cty"
)

func init() {
	hieraapi.RegisterDataHash(`terraform_backend`, TerraformBackendData)
}

var lookupLock sync.Mutex

// TerraformBackendData is a data hash function that returns values from a Terraform backend.
// The config can be any valid Terraform backend configuration.
func TerraformBackendData(_ hieraapi.ProviderContext, options map[string]px.Value) px.OrderedMap {
	// Hide Terraform's debug messages temporarily. A global mutex is required when doing
	// since only one Go routine can hide and restore at any given time.
	lookupLock.Lock()
	stdOut := log.Writer()
	log.SetOutput(ioutil.Discard)
	defer func() {
		log.SetOutput(stdOut)
		lookupLock.Unlock()
	}()

	backendName, ok := options[`backend`]
	if !ok {
		panic(px.Error(hieraapi.MissingRequiredOption, issue.H{`option`: `backend`}))
	}
	backend := backendName.String()
	workspaceName, ok := options[`workspace`]
	var workspace string
	if !ok {
		workspace = "default"
	} else {
		workspace = workspaceName.String()
	}
	configMap, ok := options[`config`]
	if !ok {
		panic(px.Error(hieraapi.MissingRequiredOption, issue.H{`option`: `config`}))
	}
	conf := make(map[string]cty.Value)
	if cm, ok := configMap.(px.OrderedMap); ok {
		cm.EachPair(func(k, v px.Value) {
			conf[k.String()] = cty.StringVal(v.String())
		})
	} else {
		panic(fmt.Errorf("%q must be a map", "config"))
	}
	config := cty.ObjectVal(conf)
	backendInit.Init(nil)
	f := backendInit.Backend(backend)
	if f == nil {
		panic(fmt.Errorf("unknown backend type %q", backend))
	}
	b := f()
	schema := b.ConfigSchema()
	configVal, err := schema.CoerceValue(config)
	if err != nil {
		panic(fmt.Errorf("the given configuration is not valid for backend %q", backend))
	}
	newVal, diags := b.PrepareConfig(configVal)
	if diags.HasErrors() {
		panic(diags.Err())
	}
	configVal = newVal
	diags = b.Configure(configVal)
	if diags.HasErrors() {
		panic(diags.Err())
	}
	state, err := b.StateMgr(workspace)
	if err != nil {
		panic(err)
	}
	err = state.RefreshState()
	if err != nil {
		panic(err)
	}
	remoteState := state.State()
	mod := remoteState.RootModule()
	output := make(map[string]interface{})
	for k, os := range mod.OutputValues {
		output[k] = hcl2shim.ConfigValueFromHCL2(os.Value)
	}
	hsh := px.Wrap(nil, output)
	return hsh.(px.OrderedMap)
}
