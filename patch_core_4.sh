sed -i 's|c.store.Read(\&decWeightsArtifact)|c.store.Read(decWeightsArtifact)|g' core.go
sed -i 's|c.store.Read(\&decMergesArtifact)|c.store.Read(decMergesArtifact)|g' core.go
sed -i 's|json.Marshal(r)|json.Encode(r)|g' protocol.go
sed -i 's|json.Unmarshal(data, \&r)|json.Decode(data, \&r)|g' protocol.go
sed -i 's|json.Marshal(e)|json.Encode(e)|g' protocol.go
sed -i 's|json.Unmarshal(data, \&e)|json.Decode(data, \&e)|g' protocol.go
sed -i 's|w, err := js.NewWorker|w := js.NewWorker|g' page.go
sed -i '/if err != nil {/,/return nil, err/d' page.go
sed -i 's|OnMessage(ctx context.Context,|OnMessage(ctx *context.Context,|g' worker.go
