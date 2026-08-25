// generic_ids_export_test.go exportiert — ausschließlich für den
// Testlauf, dieser Dateisuffix sorgt dafür, dass die Datei NIE in die
// Produktion mitgebaut wird — die Menge der über den generischen
// Deskriptor-Pfad (registerReadService/registerSync, read_generic.go/
// read_sync.go) angemeldeten Werkzeugnamen, jeweils zusammen mit der
// Tatsache, ob das zugehörige IDOf tatsächlich gesetzt ist. Dieselben
// 14 Deskriptor-Konstruktoren, die TestAlleGenerischenDeskriptorenHabenEinIDOf
// (read_generic_test.go, Aufgabe 4) bereits einzeln aufzählt und prüft
// — hier nur zusätzlich mit den tatsächlich registrierten Werkzeugnamen
// verknüpft, keine zweite, unabhängig gepflegte Liste.
//
// GenericReadToolStatus ist der Brücken-Export, den issued_coverage_test.go
// (package tools_test, Aufgabe 5) braucht, um seinen eigenen Guardrail
// (TestAlleLeseWerkzeugeSindEinsortiert) NICHT länger über eine
// handgetippte Namensliste laufen zu lassen, die nie prüft, ob ein
// Werkzeug überhaupt IDs ausliefert und erfasst.
//
// Der Riss, den dieser Export schließt (vom Review gefunden, real
// reproduziert): eine handgetippte "das ist generisch, anderswo
// abgedeckt"-Liste akzeptiert JEDEN dort eingetragenen Namen, unabhängig
// davon, ob er überhaupt zu einem echten, gesund verdrahteten
// Deskriptor gehört. Ein generischer Deskriptor mit IDOf: nil, einmal
// gemountet und in eine solche Liste eingetragen, blieb dadurch
// unbemerkt grün — bis zum ersten echten Aufruf, der wegen
// read_generic.go:444's d.IDOf(&entry) mit einer Nil-Pointer-Panik
// abstürzte und, mangels recover() irgendwo im Dispatch-Pfad des SDK
// (Issue #70), den GESAMTEN Serverprozess mitriss — nicht nur den
// aufrufenden Request. Mit diesem Export ist die Mitgliedschaft
// abgeleitet statt getippt: ein Name gilt nur dann als sicher
// "generisch, anderswo abgedeckt", wenn er tatsächlich als ListName/
// GetName/SyncName aus einem dieser 14 Konstruktoren stammt UND dessen
// IDOf gesetzt ist. Ein neuer oder kaputt verdrahteter generischer
// Deskriptor kommt nur noch durch, wenn er auch in
// TestAlleGenerischenDeskriptorenHabenEinIDOf auftaucht und dort besteht
// — und fällt dort wie hier gleichermaßen auf, statt an einer der
// beiden Prüfungen unbemerkt vorbeizulaufen.
package tools

// GenericReadToolStatus liefert für jeden Werkzeugnamen, den einer der
// 14 generischen Deskriptor-Konstruktoren (registerReferenceTools/
// registerPeopleTools/registerSyncTools) tatsächlich als ListName/
// GetName/SyncName trägt, ob das IDOf-Feld dieses Deskriptors gesetzt
// ist (true) oder nil ist (false). Ein Name, der überhaupt nicht in der
// zurückgegebenen Map auftaucht, stammt von KEINEM dieser 14
// Konstruktoren — er ist kein generisches Deskriptor-Werkzeug.
func GenericReadToolStatus() map[string]bool {
	out := map[string]bool{}
	addRead := func(listName, getName string, idOf bool) {
		out[listName] = idOf
		out[getName] = idOf
	}
	addSync := func(syncName string, idOf bool) {
		out[syncName] = idOf
	}

	rt := referenceTagDescriptor()
	addRead(rt.ListName, rt.GetName, rt.IDOf != nil)
	rc := referenceCompanyDescriptor()
	addRead(rc.ListName, rc.GetName, rc.IDOf != nil)
	rdt := referenceDocumentTypeDescriptor()
	addRead(rdt.ListName, rdt.GetName, rdt.IDOf != nil)
	rds := referenceDocumentTypeSchemeDescriptor()
	addRead(rds.ListName, rds.GetName, rds.IDOf != nil)
	cd := contactDescriptor()
	addRead(cd.ListName, cd.GetName, cd.IDOf != nil)
	rmd := reminderDescriptor()
	addRead(rmd.ListName, rmd.GetName, rmd.IDOf != nil)
	cvd := conversationDescriptor()
	addRead(cvd.ListName, cvd.GetName, cvd.IDOf != nil)

	tsd := tagSyncDescriptor()
	addSync(tsd.SyncName, tsd.IDOf != nil)
	csd := companySyncDescriptor()
	addSync(csd.SyncName, csd.IDOf != nil)
	dtsd := documentTypeSyncDescriptor()
	addSync(dtsd.SyncName, dtsd.IDOf != nil)
	dtssd := documentTypeSchemeSyncDescriptor()
	addSync(dtssd.SyncName, dtssd.IDOf != nil)
	consd := contactSyncDescriptor()
	addSync(consd.SyncName, consd.IDOf != nil)
	remsd := reminderSyncDescriptor()
	addSync(remsd.SyncName, remsd.IDOf != nil)
	convsd := conversationSyncDescriptor()
	addSync(convsd.SyncName, convsd.IDOf != nil)

	return out
}
