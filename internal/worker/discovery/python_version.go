package discovery

import "path"

// PythonRequirement reads the Python version declaration of one module
// manifest with the env.discover parser (docs/ARCHITECTURE.md): pyproject.toml
// [project] requires-python, setup.cfg [options] python_requires or the
// first version of .python-version. kind is the environment requirement
// kind (requires_python, python_requires, python_version_file); "" with a
// nil error means the file declares none. The Python pack uses it so
// the version it passes to the tools is the one discovery reports.
func PythonRequirement(p string, b []byte) (kind, constraint string, err error) {
	switch path.Base(p) {
	case "pyproject.toml", "setup.cfg", ".python-version":
	default:
		return "", "", nil
	}
	if len(b) > maxManifestBytes {
		return "", "", errMalformed
	}
	reqs, _, err := parseManifest(p, b)
	if err != nil || len(reqs) == 0 {
		return "", "", err
	}
	return reqs[0].Kind, reqs[0].Constraint, nil
}
