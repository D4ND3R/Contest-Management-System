package adminweb

import "encoding/json"

// typeFields is the union of every task type's parameters, for the
// structured dataset form (JSON names as in internal/tasktypes).
type typeFields struct {
	Compilation           string  `json:"compilation,omitempty"`
	InputFile             string  `json:"input_file,omitempty"`
	OutputFile            string  `json:"output_file,omitempty"`
	Checker               string  `json:"checker,omitempty"`
	FloatAbsTol           float64 `json:"float_abs_tol,omitempty"`
	FloatRelTol           float64 `json:"float_rel_tol,omitempty"`
	CheckerTimeLimitMs    int64   `json:"checker_time_limit_ms,omitempty"`
	CheckerMemoryBytes    int64   `json:"checker_memory_bytes,omitempty"`
	OutputPattern         string  `json:"output_pattern,omitempty"`
	MergePrevious         bool    `json:"merge_previous,omitempty"`
	Manager               string  `json:"manager,omitempty"`
	NumProcesses          int     `json:"num_processes,omitempty"`
	UserIO                string  `json:"user_io,omitempty"`
	LimitsMode            string  `json:"limits_mode,omitempty"`
	ManagerTimeLimitMs    int64   `json:"manager_time_limit_ms,omitempty"`
	ManagerMemoryBytes    int64   `json:"manager_memory_bytes,omitempty"`
	InteractorTimeLimitMs int64   `json:"interactor_time_limit_ms,omitempty"`
	InteractorMemoryBytes int64   `json:"interactor_memory_bytes,omitempty"`
}

// CheckerMemMiB and friends show byte limits in MiB.
func (t typeFields) CheckerMemMiB() int64    { return t.CheckerMemoryBytes >> 20 }
func (t typeFields) ManagerMemMiB() int64    { return t.ManagerMemoryBytes >> 20 }
func (t typeFields) InteractorMemMiB() int64 { return t.InteractorMemoryBytes >> 20 }

func typeFieldsOf(raw json.RawMessage) typeFields {
	var tf typeFields
	_ = json.Unmarshal(raw, &tf)
	return tf
}

// buildTypeParams turns the structured fields of taskType into
// task_type_params, keeping only the parameters that type understands.
func buildTypeParams(f *form, taskType string) json.RawMessage {
	m := map[string]any{}
	set := func(key string, v any) {
		switch x := v.(type) {
		case string:
			if x != "" {
				m[key] = x
			}
		case int64:
			if x != 0 {
				m[key] = x
			}
		case float64:
			if x != 0 {
				m[key] = x
			}
		case bool:
			if x {
				m[key] = x
			}
		}
	}
	checker := func() {
		set("checker", f.str("tt_checker"))
		if x, ok := f.float("tt_float_abs_tol", "Absolute tolerance"); ok {
			set("float_abs_tol", x)
		}
		if x, ok := f.float("tt_float_rel_tol", "Relative tolerance"); ok {
			set("float_rel_tol", x)
		}
		set("checker_time_limit_ms", f.int64("tt_checker_time_limit_ms", "Checker time limit", 0))
		set("checker_memory_bytes", f.int64("tt_checker_memory_mib", "Checker memory", 0)<<20)
	}
	switch taskType {
	case "Batch":
		set("compilation", f.str("tt_compilation"))
		set("input_file", f.str("tt_input_file"))
		set("output_file", f.str("tt_output_file"))
		checker()
	case "OutputOnly":
		set("output_pattern", f.str("tt_output_pattern"))
		set("merge_previous", f.check("tt_merge_previous"))
		checker()
	case "TwoSteps":
		set("manager", f.str("tt_manager"))
		checker()
	case "Communication":
		set("num_processes", f.int64("tt_num_processes", "Processes", 1))
		set("compilation", f.str("tt_comm_compilation"))
		set("user_io", f.str("tt_user_io"))
		set("limits_mode", f.str("tt_limits_mode"))
		set("manager_time_limit_ms", f.int64("tt_manager_time_limit_ms", "Manager time limit", 0))
		set("manager_memory_bytes", f.int64("tt_manager_memory_mib", "Manager memory", 0)<<20)
	case "Interactive":
		set("interactor_time_limit_ms", f.int64("tt_interactor_time_limit_ms", "Interactor time limit", 0))
		set("interactor_memory_bytes", f.int64("tt_interactor_memory_mib", "Interactor memory", 0)<<20)
	}
	raw, _ := json.Marshal(m)
	return raw
}
