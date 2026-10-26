package catalog

import "strings"

// SoftwareProfile describes a supported service's execution and access contract.
// Ports are backend ports; the coordinator assigns an independent external port.
type SoftwareProfile struct {
	Software     string   `json:"software"`
	DisplayName  string   `json:"displayName"`
	Aliases      []string `json:"aliases"`
	WorkloadKind string   `json:"workloadKind"`
	BackendPort  int      `json:"backendPort"`
	Protocol     string   `json:"protocol"`
	Persistent   bool     `json:"persistent"`
}

func Profiles() []SoftwareProfile {
	return []SoftwareProfile{
		{"redis", "Redis", []string{"redis"}, "StatefulSet", 6379, "redis", true},
		{"postgres", "PostgreSQL", []string{"postgres", "postgresql", "pg"}, "StatefulSet", 5432, "postgres", true},
		{"mysql", "MySQL", []string{"mysql"}, "StatefulSet", 3306, "mysql", true},
		{"mongodb", "MongoDB", []string{"mongodb", "mongo"}, "StatefulSet", 27017, "mongodb", true},
		{"milvus", "Milvus Standalone", []string{"milvus"}, "StatefulSet", 19530, "milvus", true},
		{"nginx", "Nginx", []string{"nginx"}, "Deployment", 80, "http", false},
		{"sandbox", "Python JupyterLab sandbox", []string{"sandbox", "python", "jupyter", "jupyterlab", "python-sandbox", "沙箱", "代码沙箱"}, "Deployment", 8888, "http", false},
	}
}

func NormalizeSoftware(software string) string {
	software = strings.ToLower(strings.TrimSpace(software))
	for _, profile := range Profiles() {
		for _, alias := range profile.Aliases {
			if software == alias {
				return profile.Software
			}
		}
	}
	return software
}

func Profile(software string) (SoftwareProfile, bool) {
	software = NormalizeSoftware(software)
	for _, profile := range Profiles() {
		if profile.Software == software {
			return profile, true
		}
	}
	return SoftwareProfile{}, false
}
