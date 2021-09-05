package zookeeper

type ZkInstanceData struct {
	ServiceType string `json:"serviceType" gorm:"column:serviceType"`
	Address     string `json:"address" gorm:"column:address"`
	Port        int    `json:"port" gorm:"column:port"`
	Payload     struct {
		Metadata struct {
			InstanceStatus string `json:"instance_status" gorm:"column:instance_status"`
		} `json:"metadata" gorm:"column:metadata"`
		Class string `json:"@class" gorm:"column:@class"`
		Name  string `json:"name" gorm:"column:name"`
		ID    string `json:"id" gorm:"column:id"`
	} `json:"payload" gorm:"column:payload"`
	RegistrationTimeUTC int64 `json:"registrationTimeUTC" gorm:"column:registrationTimeUTC"`
	UriSpec             struct {
		Parts []struct {
			Variable bool   `json:"variable" gorm:"column:variable"`
			Value    string `json:"value" gorm:"column:value"`
		} `json:"parts" gorm:"column:parts"`
	} `json:"uriSpec" gorm:"column:uriSpec"`
	Name    string      `json:"name" gorm:"column:name"`
	ID      string      `json:"id" gorm:"column:id"`
	SslPort interface{} `json:"sslPort" gorm:"column:sslPort"`
}
