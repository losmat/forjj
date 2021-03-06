package main

import (
	"bytes"
	"fmt"
	"github.com/forj-oss/forjj-modules/cli"
	"github.com/forj-oss/forjj-modules/trace"
	"github.com/forj-oss/goforjj"
	"log"
	"os"
	"path"
	"text/template"
)

// Load driver options to a Command requested.

// Currently there is no distinction about setting different options for a specific task on the driver.
func (a *Forj) load_driver_options(instance_name string) error {
	if err := a.read_driver(instance_name); err != nil {
		return err
	}

	if a.drivers[instance_name].plugin.Yaml.Name != "" { // if true => Driver Def loaded
		a.init_driver_flags(instance_name)
	}

	return nil
}

func (d *Driver) Model() (m *DriverModel) {
	m = &DriverModel{
		InstanceName: d.InstanceName,
		Name:         d.Name,
	}
	return
}

// TODO: Check if forjj-options, plugins runtime are valid or not.

func (a *Forj) load_missing_drivers() error {
	gotrace.Trace("Number of registered instances %d", len(a.o.Drivers))
	gotrace.Trace("Number of loaded instances %d", len(a.drivers))
	// TODO: Use data loaded from configuration saved in source 'forjj-options.yaml' and update it from driver, instead of reloading from scratch.
	for instance, d := range a.o.Drivers {
		if _, found := a.drivers[instance]; !found {
			gotrace.Trace("Loading missing instance %s", instance)
			a.drivers[instance] = d
			d.cmds = map[string]DriverCmdOptions{ // List of Driver actions supported.
				"common":   {make(map[string]DriverCmdOptionFlag)},
				"create":   {make(map[string]DriverCmdOptionFlag)},
				"update":   {make(map[string]DriverCmdOptionFlag)},
				"maintain": {make(map[string]DriverCmdOptionFlag)},
			}

			gotrace.Trace("Loading '%s'", instance)
			if err := a.load_driver_options(instance); err != nil {
				log.Printf("Unable to load plugin information for instance '%s'. %s", instance, err)
				continue
			}
			// Complete the driver information in cli records
			// The instance record has been created automatically with  cli.ForjObject.AddInstanceField()
			a.cli.SetValue(app, d.Name, cli.String, "type", d.DriverType)
			a.cli.SetValue(app, d.Name, cli.String, "driver", d.Name)
			/*            if err := d.plugin.PluginLoadFrom(instance, d.Runtime) ; err != nil {
			              log.Printf("Unable to load Runtime information from forjj-options for instance '%s'. Forjj may not work properly. You can fix it with 'forjj update --apps %s:%s:%s'. %s", instance, d.DriverType, d.Name, d.InstanceName, err)
			          }*/
			d.plugin.PluginSetWorkspace(a.w.Path())
			d.plugin.PluginSetSource(path.Join(a.w.Path(), a.w.Infra.Name, "apps", d.DriverType))
			d.plugin.PluginSocketPath(path.Join(a.w.Path(), "lib"))
		}
	}
	return nil
}

// Read Driver yaml document
func (a *Forj) read_driver(instance_name string) (err error) {
	var (
		yaml_data []byte
		driver    *Driver
	)
	if d, ok := a.drivers[instance_name]; ok {
		driver = d
	}

	if driver.Name == "" {
		return
	}

	ContribRepoUri := *a.ContribRepo_uri
	ContribRepoUri.Path = path.Join(ContribRepoUri.Path, driver.DriverType, driver.Name, driver.Name+".yaml")

	if yaml_data, err = read_document_from(&ContribRepoUri); err != nil {
		return
	}

	if err = driver.plugin.PluginDefLoad(yaml_data); err != nil {
		return
	}

	// Set defaults value for undefined parameters
	var ff string
	if driver.plugin.Yaml.CreatedFile == "" {
		ff = "." + driver.InstanceName + ".created"
		driver.ForjjFlagFile = true // Forjj will test the creation success itself, as the driver did not created it automatically.
	} else {
		ff = driver.plugin.Yaml.CreatedFile
	}

	// Initialized defaults value from templates
	var doc bytes.Buffer

	if t, err := template.New("plugin").Parse(ff); err != nil {
		return fmt.Errorf("Unable to interpret plugin yaml definition. '/created_flag_file' has an invalid template string '%s'. %s", driver.plugin.Yaml.CreatedFile, err)
	} else {
		t.Execute(&doc, driver.Model())
	}
	driver.FlagFile = doc.String()
	driver.Runtime = &driver.plugin.Yaml.Runtime
	gotrace.Trace("Created flag file name Set to default for plugin instance '%s' to %s", driver.InstanceName, driver.plugin.Yaml.CreatedFile)

	return

}

func (a *Forj) get_valid_driver_actions() (validActions []string) {
	actions := a.cli.GetAllActions()
	validActions = make([]string, 0, len(actions))
	for action_name := range actions {
		if inStringList(action_name, cr_act, upd_act, maint_act) == "" {
			validActions = append(validActions, action_name)
		}
	}
	return
}

// Initialize command drivers flags with plugin definition loaded from plugin yaml file.
func (a *Forj) init_driver_flags(instance_name string) {
	d := a.drivers[instance_name]
	service_type := d.DriverType
	opts := a.drivers_options.Drivers[instance_name]
	id := initDriverObjectFlags{
		a:             a,
		d:             a.drivers[instance_name],
		instance_name: instance_name,
		d_opts:        &opts,
	}

	gotrace.Trace("Setting create/update/maintain flags from plugin type '%s' (%s)", service_type, d.plugin.Yaml.Name)
	for command, flags := range d.plugin.Yaml.Tasks {
		id.set_task_flags(command, flags)
	}

	// Create an object or enhance an existing one.
	// Then create the object key if needed.
	// Then add fields, define actions and create flags.
	gotrace.Trace("Setting Objects...")
	for object_name, object_det := range d.plugin.Yaml.Objects {
		new := id.determine_object(object_name, &object_det)

		// Determine which actions can be configured for drivers object flags.
		id.prepare_actions_list()

		gotrace.Trace("Object '%s': Adding fields", object_name)
		// Adding fields to the object.
		for flag_name, flag_det := range object_det.Flags {
			if id.add_object_fields(flag_name, &flag_det, id.validActions) {
				object_det.Flags[flag_name] = flag_det
			}
		}

		gotrace.Trace("Object '%s': Adding groups fields", object_name)
		for group_name, group_det := range object_det.Groups {
			default_actions := id.validActions
			if group_det.Actions != nil && len(group_det.Actions) > 0 {
				default_actions = group_det.Actions
				gotrace.Trace("Object '%s' - Group '%s': Default group actions defined to '%s'", default_actions)
			}

			for flag_name, flag_det := range group_det.Flags {
				if id.add_object_fields(group_name+"-"+flag_name, &flag_det, default_actions) {
					object_det.Groups[group_name].Flags[flag_name] = flag_det
				}
			}
		}

		if new {
			gotrace.Trace("Object '%s': Setting Object supported Actions...", object_name)
			// Adding Actions to the object.
			if len(id.add_object_actions()) == 0 {
				gotrace.Warning("No actions to add flags.")
				continue
			}
		} else {
			gotrace.Trace("Object '%s': Supported Actions already set - Not a new object.", object_name)
		}

		gotrace.Trace("Object '%s': Adding Object Action flags...", object_name)
		// Adding flags to object actions
		for flag_name, flag_dets := range object_det.Flags {
			id.add_object_actions_flags(flag_name, flag_dets, id.validActions)
		}
		gotrace.Trace("Object '%s': Adding Object Action groups flags", object_name)
		for group_name, group_det := range object_det.Groups {
			default_actions := id.validActions
			if group_det.Actions != nil && len(group_det.Actions) > 0 {
				default_actions = group_det.Actions
			}
			for flag_name, flag_det := range group_det.Flags {
				id.add_object_actions_flags(group_name+"-"+flag_name, flag_det, default_actions)
			}
		}

	}

	// TODO: Give plugin capability to manipulate new plugin object instances as list (ex: role => roles)
	// TODO: integrate new plugins objects list in create/update task
}

// Set options on a new flag created.
//
// It currently assigns defaults or required.
//
func (d *DriverOptions) set_flag_options(option_name string, params *goforjj.YamlFlagOptions) (opts *cli.ForjOpts) {
	if params == nil {
		return
	}

	var preloaded_data bool
	opts = cli.Opts()

	if d != nil {
		if option_value, found := d.Options[option_name]; found && option_value.Value != "" {
			// Do not set flag in any case as required or with default, if a value has been set in the driver loaded options (creds-forjj.yml)
			preloaded_data = true
			if params.Secure {
				// We do not set a secure data as default in kingpin default flags to avoid displaying them from forjj help.
				gotrace.Trace("Option value found for '%s' : -- set as hidden default value. --", option_name)
				// The data will be retrieved by
			} else {
				gotrace.Trace("Option value found for '%s' : %s -- Default value. --", option_name, option_value.Value)
				// But here, we can show through kingpin default what was loaded.
				opts.Default(option_value.Value)
			}
		}
	}

	if !preloaded_data {
		// No preloaded data from forjj-creds.yaml (or equivalent files) -- Normal plugin driver set up
		if params.Required {
			opts.Required()
		}
		if params.Default != "" {
			opts.Default(params.Default)
		}
	}

	if params.Envar != "" {
		opts.Envar(params.Envar)
	}
	return
}

// Create the flag to a kingpin Command. (create/update/maintain)
func (d *Driver) init_driver_flags_for(a *Forj, option_name, command, forjj_option_name, forjj_option_help string, opts *cli.ForjOpts) {
	if command == "" {
		// Add to the Application layer.
		gotrace.Trace("Set App flag '%s(%s)'", forjj_option_name, option_name)
		a.cli.AddAppFlag(cli.String, forjj_option_name, forjj_option_help, opts)
		return
	}
	// No value by default. Will be set later after complete parse.
	d.cmds[command].flags[forjj_option_name] = DriverCmdOptionFlag{driver_flag_name: option_name}

	// Create flag 'option_name' on kingpin cmd or app
	if forjj_option_name != option_name {
		gotrace.Trace("Set action '%s' flag '%s(%s)'", command, forjj_option_name, option_name)
	} else {
		gotrace.Trace("Set action '%s' flag '%s'", command, forjj_option_name)
	}
	a.cli.OnActions(command).AddFlag(cli.String, forjj_option_name, forjj_option_help, opts)
	return
}

// GetDriversFlags - cli App context hook. Load drivers requested (app object)
// This function is provided as cli app object Parse hook
func (a *Forj) GetDriversFlags(o *cli.ForjObject, c *cli.ForjCli, _ interface{}) (error, bool) {
	list := a.cli.GetObjectValues(o.Name())
	// Loop on drivers to pre-initialized drivers flags.
	gotrace.Trace("Number of plugins provided from parameters: %d", len(list))
	for _, d := range list {
		driver := d.GetString("driver")
		driver_type := d.GetString("type")
		instance := d.GetString("name")
		if driver == "" || driver_type == "" {
			gotrace.Trace("Invalid plugin definition. driver:%s, driver_type:%s", driver, driver_type)
			continue
		}

		a.drivers[instance] = &Driver{
			Name:         driver,
			DriverType:   driver_type,
			InstanceName: instance,
			app_request:  true,
			cmds: map[string]DriverCmdOptions{ // List of Driver actions supported.
				"common":   {make(map[string]DriverCmdOptionFlag)},
				"create":   {make(map[string]DriverCmdOptionFlag)},
				"update":   {make(map[string]DriverCmdOptionFlag)},
				"maintain": {make(map[string]DriverCmdOptionFlag)},
			},
		}
		gotrace.Trace("Selected '%s' app driver: %s\n", driver_type, driver)

		if err := a.load_driver_options(instance); err != nil {
			fmt.Printf("Error: %#v\n", err)
			os.Exit(1)
		}
	}

	// Automatically load all other drivers not requested by --apps but listed in forjj-options.yaml.
	// Those drivers are all used by all services that forjj should manage.
	a.load_missing_drivers()
	return nil, true
}

// GetForjjFlags build the Forjj list of parameters requested by the plugin for a specific action name.
func (a *Forj) GetForjjFlags(r *goforjj.PluginReqData, d *Driver, action string) {
	if tc, found := d.plugin.Yaml.Tasks[action]; found {
		for flag_name := range tc {
			if v, found := a.GetDriversActionsParameter(d, flag_name); found {
				r.Forj[flag_name] = v
			}
		}
	}
}

// GetObjectsData build the list of Object required by the plugin provided from the cli flags.
func (a *Forj) GetObjectsData(r *goforjj.PluginReqData, d *Driver, action string) {
	// Loop on each plugin object
	for object_name := range d.plugin.Yaml.Objects {
		for instance_name, instance_data := range a.cli.GetObjectValues(object_name) {
			ia := make(goforjj.InstanceActions)
			keys := make(goforjj.ActionKeys)
			for key := range instance_data.Attrs() {
				if key == "action" {
					continue
				}
				if _, found, err := instance_data.Get(key); !found {
					gotrace.Trace("%s", err)
					continue
				}
				keys.AddKey(key, instance_data.GetString(key))
			}
			if action == maint_act {
				// Everything is sent as "setup" action
				ia.AddAction("setup", keys)
			} else {
				ia.AddAction(instance_data.GetString("action"), keys)
			}
			r.AddObjectActions(object_name, instance_name, ia)
		}
	}
}
