sed -i 's|ok := c.store.Has(\&decWeightsArtifact)|ok := c.store.Has(decWeightsArtifact)|g' core.go
sed -i 's|ok := c.store.Has(\&decMergesArtifact)|ok := c.store.Has(decMergesArtifact)|g' core.go
sed -i 's|ok := c.store.Has(writerWeightsArtifact)|ok := c.store.Has(\*writerWeightsArtifact)|g' core.go
sed -i 's|ok := c.store.Has(writerMergesArtifact)|ok := c.store.Has(\*writerMergesArtifact)|g' core.go
sed -i 's|return c.store.Ensure(a, p, func|return c.store.Ensure(\*a, p, func|g' core.go
sed -i 's|err = c.store.Read(\&decWeightsArtifact)|err = c.store.Read(decWeightsArtifact)|g' core.go
sed -i 's|err = c.store.Read(\&decMergesArtifact)|err = c.store.Read(decMergesArtifact)|g' core.go
sed -i 's|c.store.Read(writerWeightsArtifact)|c.store.Read(\*writerWeightsArtifact)|g' core.go
sed -i 's|c.store.Read(writerMergesArtifact)|c.store.Read(\*writerMergesArtifact)|g' core.go

sed -i 's|c.store.Prune(keep)|c.store.Prune(nil)|g' core.go
