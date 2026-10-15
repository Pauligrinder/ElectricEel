#!/usr/bin/env python3
"""Fill audit-era UI messages, preserving existing context-specific translations.

Run once, then tools/build-qm.sh to merge and compile the catalogs. Command
labels reuse their existing CommandCatalog translations explicitly; other
messages are keyed by source below. Protocol event names and Automagic's
external menu labels must remain literal.
"""
from collections import Counter
from pathlib import Path
import re
import xml.etree.ElementTree as ET

CATALOGS = Path(__file__).resolve().parent.parent / "app" / "translations"
SOURCES = [
    "Retry",
    "Control helper unavailable",
    "No key: generate and pair a key first",
    "No keys listed",
    "Exported to %1. In Automagic, use States → Import to select this folder, then Reload Daemon.",
    "Automagic export failed: %1",
    "%1 s",
    "Cover actions",
    "Left action",
    "Right action",
    "Export hotspot flows to Documents, then import them in Automagic. Automagic turns the hotspot on for presence_inside and off for presence_away_confirmed. ElectricEel emits the latter 3 minutes after presence_far unless presence_inside arrives first; repeated presence_far events do not restart the timer. ElectricEel must stay running.",
    "Export Automagic flows",
    "Exporting Automagic flows...",
    "If you used Add Automagic flows in an earlier version, disable its old hotspot flows before importing this preset. Automagic lets you resolve matching IDs during import. Extra triggers are included for your own flows, such as launching an app.",
]

# One line per SOURCES entry. A newline is only a data separator, not UI text.
TRANSLATIONS = {
    "it": """Riprova
Servizio di controllo non disponibile
Nessuna chiave: genera e associa prima una chiave
Nessuna chiave elencata
Esportato in %1. In Automagic, usa States → Import per selezionare questa cartella, poi Reload Daemon.
Esportazione Automagic fallita: %1
%1 s
Azioni della copertina
Azione sinistra
Azione destra
Esporta i flussi dell'hotspot in Documenti, poi importali in Automagic. Automagic attiva l'hotspot con presence_inside e lo disattiva con presence_away_confirmed. ElectricEel emette quest'ultimo evento 3 minuti dopo presence_far, a meno che presence_inside non arrivi prima; gli eventi presence_far ripetuti non riavviano il timer. ElectricEel deve rimanere in esecuzione.
Esporta flussi Automagic
Esportazione dei flussi Automagic...
Se hai usato Aggiungi flussi Automagic in una versione precedente, disattiva i vecchi flussi dell'hotspot prima di importare questa configurazione. Automagic consente di risolvere i conflitti tra ID durante l'importazione. Sono inclusi altri eventi di attivazione per i tuoi flussi, ad esempio per avviare un'app.""",
    "de": """Erneut versuchen
Steuerungsdienst nicht verfügbar
Kein Schlüssel: Zuerst einen Schlüssel erzeugen und koppeln
Keine Schlüssel aufgelistet
Nach %1 exportiert. In Automagic mit States → Import diesen Ordner auswählen, dann Reload Daemon verwenden.
Automagic-Export fehlgeschlagen: %1
%1 s
Cover-Aktionen
Linke Aktion
Rechte Aktion
Hotspot-Flows nach Dokumente exportieren und anschließend in Automagic importieren. Automagic schaltet den Hotspot bei presence_inside ein und bei presence_away_confirmed aus. ElectricEel sendet letzteres Ereignis 3 Minuten nach presence_far, sofern presence_inside nicht vorher eintrifft; wiederholte presence_far-Ereignisse starten den Timer nicht neu. ElectricEel muss weiterlaufen.
Automagic-Flows exportieren
Automagic-Flows werden exportiert...
Falls du in einer früheren Version Automagic-Flows hinzufügen verwendet hast, deaktiviere die alten Hotspot-Flows vor dem Import dieser Vorlage. Automagic lässt dich beim Import Konflikte zwischen gleichen IDs auflösen. Zusätzliche Auslöser für eigene Flows, etwa zum Starten einer App, sind enthalten.""",
    "fr": """Réessayer
Service de contrôle indisponible
Aucune clé : générez et associez d'abord une clé
Aucune clé répertoriée
Exporté vers %1. Dans Automagic, utilisez States → Import pour sélectionner ce dossier, puis Reload Daemon.
Échec de l'exportation Automagic : %1
%1 s
Actions de la couverture
Action de gauche
Action de droite
Exportez les flux du point d'accès vers Documents, puis importez-les dans Automagic. Automagic active le point d'accès pour presence_inside et le désactive pour presence_away_confirmed. ElectricEel émet ce dernier événement 3 minutes après presence_far, sauf si presence_inside arrive avant ; les événements presence_far répétés ne redémarrent pas le minuteur. ElectricEel doit rester en cours d'exécution.
Exporter les flux Automagic
Exportation des flux Automagic...
Si vous avez utilisé Ajouter des flux Automagic dans une version antérieure, désactivez ses anciens flux de point d'accès avant d'importer cette configuration. Automagic permet de résoudre les conflits d'identifiants lors de l'importation. Des déclencheurs supplémentaires sont inclus pour vos propres flux, par exemple pour lancer une application.""",
    "es": """Reintentar
Servicio de control no disponible
No hay clave: primero genera y vincula una clave
No se muestran claves
Exportado a %1. En Automagic, usa States → Import para seleccionar esta carpeta y después Reload Daemon.
Error al exportar a Automagic: %1
%1 s
Acciones de la cubierta
Acción izquierda
Acción derecha
Exporta los flujos del punto de acceso a Documentos y luego impórtalos en Automagic. Automagic activa el punto de acceso con presence_inside y lo desactiva con presence_away_confirmed. ElectricEel emite este último evento 3 minutos después de presence_far, salvo que presence_inside llegue antes; los eventos presence_far repetidos no reinician el temporizador. ElectricEel debe seguir ejecutándose.
Exportar flujos de Automagic
Exportando flujos de Automagic...
Si usaste Añadir flujos de Automagic en una versión anterior, desactiva sus antiguos flujos del punto de acceso antes de importar esta configuración. Automagic permite resolver conflictos entre identificadores coincidentes durante la importación. Se incluyen activadores adicionales para tus propios flujos, como iniciar una aplicación.""",
    "pt": """Tentar novamente
Serviço de controlo indisponível
Sem chave: primeiro gere e emparelhe uma chave
Nenhuma chave listada
Exportado para %1. No Automagic, use States → Import para selecionar esta pasta e depois Reload Daemon.
Falha na exportação do Automagic: %1
%1 s
Ações da capa
Ação esquerda
Ação direita
Exporte os fluxos do ponto de acesso para Documentos e depois importe-os no Automagic. O Automagic liga o ponto de acesso com presence_inside e desliga-o com presence_away_confirmed. O ElectricEel emite este último evento 3 minutos após presence_far, a menos que presence_inside chegue primeiro; eventos presence_far repetidos não reiniciam o temporizador. O ElectricEel tem de continuar em execução.
Exportar fluxos do Automagic
A exportar fluxos do Automagic...
Se usou Adicionar fluxos do Automagic numa versão anterior, desative os fluxos antigos do ponto de acesso antes de importar esta configuração. O Automagic permite resolver conflitos entre identificadores iguais durante a importação. Estão incluídos acionadores adicionais para os seus próprios fluxos, como iniciar uma aplicação.""",
    "pt_BR": """Tentar novamente
Serviço de controle indisponível
Sem chave: primeiro gere e pareie uma chave
Nenhuma chave listada
Exportado para %1. No Automagic, use States → Import para selecionar esta pasta e depois Reload Daemon.
Falha na exportação do Automagic: %1
%1 s
Ações da capa
Ação esquerda
Ação direita
Exporte os fluxos do ponto de acesso para Documentos e depois importe-os no Automagic. O Automagic liga o ponto de acesso com presence_inside e o desliga com presence_away_confirmed. O ElectricEel emite esse último evento 3 minutos após presence_far, a menos que presence_inside chegue primeiro; eventos presence_far repetidos não reiniciam o temporizador. O ElectricEel precisa continuar em execução.
Exportar fluxos do Automagic
Exportando fluxos do Automagic...
Se você usou Adicionar fluxos do Automagic em uma versão anterior, desative os fluxos antigos do ponto de acesso antes de importar esta configuração. O Automagic permite resolver conflitos entre IDs iguais durante a importação. Estão incluídos gatilhos adicionais para seus próprios fluxos, como iniciar um aplicativo.""",
    "nl": """Opnieuw proberen
Besturingsdienst niet beschikbaar
Geen sleutel: maak en koppel eerst een sleutel
Geen sleutels vermeld
Geëxporteerd naar %1. Gebruik in Automagic States → Import om deze map te selecteren en daarna Reload Daemon.
Automagic-export mislukt: %1
%1 s
Omslagacties
Linkeractie
Rechteractie
Exporteer hotspotflows naar Documenten en importeer ze vervolgens in Automagic. Automagic schakelt de hotspot in bij presence_inside en uit bij presence_away_confirmed. ElectricEel verstuurt die laatste gebeurtenis 3 minuten na presence_far, tenzij presence_inside eerder binnenkomt; herhaalde presence_far-gebeurtenissen starten de timer niet opnieuw. ElectricEel moet actief blijven.
Automagic-flows exporteren
Automagic-flows exporteren...
Als je in een eerdere versie Automagic-flows toevoegen hebt gebruikt, schakel dan de oude hotspotflows uit voordat je deze voorinstelling importeert. Automagic laat je tijdens het importeren conflicten met gelijke ID's oplossen. Er zijn extra triggers voor je eigen flows inbegrepen, zoals het starten van een app.""",
    "da": """Prøv igen
Styringstjenesten er ikke tilgængelig
Ingen nøgle: Opret og par først en nøgle
Ingen nøgler angivet
Eksporteret til %1. Brug States → Import i Automagic til at vælge denne mappe og derefter Reload Daemon.
Automagic-eksport mislykkedes: %1
%1 s
Coverhandlinger
Venstre handling
Højre handling
Eksportér hotspotforløb til Dokumenter, og importér dem derefter i Automagic. Automagic tænder hotspottet ved presence_inside og slukker det ved presence_away_confirmed. ElectricEel udsender sidstnævnte hændelse 3 minutter efter presence_far, medmindre presence_inside kommer først; gentagne presence_far-hændelser genstarter ikke timeren. ElectricEel skal fortsat køre.
Eksportér Automagic-forløb
Eksporterer Automagic-forløb...
Hvis du brugte Tilføj Automagic-forløb i en tidligere version, skal du deaktivere de gamle hotspotforløb, før du importerer denne forudindstilling. Automagic lader dig løse konflikter mellem ens ID'er under importen. Der medfølger ekstra udløsere til dine egne forløb, f.eks. til at starte en app.""",
    "nb": """Prøv igjen
Styringstjenesten er utilgjengelig
Ingen nøkkel: Opprett og par først en nøkkel
Ingen nøkler oppført
Eksportert til %1. Bruk States → Import i Automagic for å velge denne mappen, og deretter Reload Daemon.
Automagic-eksport mislyktes: %1
%1 s
Omslagshandlinger
Venstre handling
Høyre handling
Eksporter Wi-Fi-soneflyter til Dokumenter, og importer dem deretter i Automagic. Automagic slår på Wi-Fi-sonen ved presence_inside og av ved presence_away_confirmed. ElectricEel sender sistnevnte hendelse 3 minutter etter presence_far, med mindre presence_inside kommer først; gjentatte presence_far-hendelser starter ikke tidtakeren på nytt. ElectricEel må fortsette å kjøre.
Eksporter Automagic-flyter
Eksporterer Automagic-flyter...
Hvis du brukte Legg til Automagic-flyter i en tidligere versjon, må du deaktivere de gamle Wi-Fi-soneflytene før du importerer denne forhåndsinnstillingen. Automagic lar deg løse konflikter mellom like ID-er under importen. Ekstra utløsere for egne flyter, som å starte en app, er inkludert.""",
    "sv": """Försök igen
Styrningstjänsten är inte tillgänglig
Ingen nyckel: Skapa och parkoppla först en nyckel
Inga nycklar listade
Exporterat till %1. Använd States → Import i Automagic för att välja den här mappen och sedan Reload Daemon.
Automagic-exporten misslyckades: %1
%1 s
Omslagsåtgärder
Vänster åtgärd
Höger åtgärd
Exportera hotspotflöden till Dokument och importera dem sedan i Automagic. Automagic aktiverar hotspoten vid presence_inside och stänger av den vid presence_away_confirmed. ElectricEel skickar den senare händelsen 3 minuter efter presence_far, om inte presence_inside kommer först; upprepade presence_far-händelser startar inte om timern. ElectricEel måste fortsätta köras.
Exportera Automagic-flöden
Exporterar Automagic-flöden...
Om du använde Lägg till Automagic-flöden i en tidigare version ska du inaktivera de gamla hotspotflödena innan du importerar den här förinställningen. Automagic låter dig lösa konflikter mellan likadana ID:n vid importen. Extra utlösare för dina egna flöden, till exempel för att starta en app, ingår.""",
    "fi": """Yritä uudelleen
Ohjauspalvelu ei ole käytettävissä
Ei avainta: luo ja parita ensin avain
Ei lueteltuja avaimia
Viety kohteeseen %1. Valitse tämä kansio Automagicissa kohdasta States → Import ja valitse sitten Reload Daemon.
Automagic-vienti epäonnistui: %1
%1 s
Kansitoiminnot
Vasen toiminto
Oikea toiminto
Vie yhteyspisteen työnkulut Asiakirjoihin ja tuo ne sitten Automagiciin. Automagic kytkee yhteyspisteen päälle tapahtumasta presence_inside ja pois tapahtumasta presence_away_confirmed. ElectricEel lähettää jälkimmäisen 3 minuuttia presence_far-tapahtuman jälkeen, ellei presence_inside saavu ensin; toistuvat presence_far-tapahtumat eivät käynnistä ajastinta uudelleen. ElectricEelin on pysyttävä käynnissä.
Vie Automagic-työnkulut
Viedään Automagic-työnkulkuja...
Jos käytit aiemmassa versiossa Lisää Automagic-työnkulut -toimintoa, poista vanhat yhteyspisteen työnkulut käytöstä ennen tämän esiasetuksen tuontia. Automagicissa voit ratkaista samojen tunnisteiden ristiriidat tuonnin aikana. Mukana on lisälaukaisimia omille työnkuluillesi, esimerkiksi sovelluksen käynnistämiseen.""",
    "et": """Proovi uuesti
Juhtimisteenus pole saadaval
Võti puudub: esmalt loo ja seo võti
Võtmeid pole loetletud
Eksporditud asukohta %1. Vali Automagicus see kaust menüüst States → Import ja seejärel Reload Daemon.
Automagicu eksport ebaõnnestus: %1
%1 s
Katte toimingud
Vasak toiming
Parem toiming
Ekspordi pääsupunkti töövood kausta Dokumendid ja impordi need seejärel Automagicusse. Automagic lülitab pääsupunkti sisse sündmusega presence_inside ja välja sündmusega presence_away_confirmed. ElectricEel väljastab viimase 3 minutit pärast sündmust presence_far, kui presence_inside ei saabu varem; korduvad presence_far-sündmused ei taaskäivita taimerit. ElectricEel peab jääma tööle.
Ekspordi Automagicu töövood
Automagicu töövoogude eksportimine...
Kui kasutasid varasemas versioonis funktsiooni Lisa Automagicu töövood, keela selle vanad pääsupunkti töövood enne selle eelseadistuse importimist. Automagic võimaldab importimisel lahendada kattuvate ID-de konfliktid. Kaasas on lisapäästikud sinu enda töövoogude jaoks, näiteks rakenduse käivitamiseks.""",
    "lv": """Mēģināt vēlreiz
Vadības pakalpojums nav pieejams
Nav atslēgas: vispirms izveidojiet un savienojiet pārī atslēgu
Nav uzskaitītu atslēgu
Eksportēts uz %1. Automagic izmantojiet States → Import, lai atlasītu šo mapi, pēc tam Reload Daemon.
Automagic eksportēšana neizdevās: %1
%1 s
Vāka darbības
Kreisā darbība
Labā darbība
Eksportējiet tīklāja plūsmas uz Dokumentiem, pēc tam importējiet tās Automagic. Automagic ieslēdz tīklāju pie presence_inside un izslēdz pie presence_away_confirmed. ElectricEel izsūta pēdējo notikumu 3 minūtes pēc presence_far, ja vien presence_inside nepienāk agrāk; atkārtoti presence_far notikumi neatsāk taimeri. ElectricEel jāpaliek ieslēgtam.
Eksportēt Automagic plūsmas
Eksportē Automagic plūsmas...
Ja iepriekšējā versijā izmantojāt Pievienot Automagic plūsmas, pirms šī sākotnējā iestatījuma importēšanas atspējojiet vecās tīklāja plūsmas. Automagic importēšanas laikā ļauj atrisināt vienādu ID konfliktus. Iekļauti papildu aktivizētāji jūsu plūsmām, piemēram, lietotnes palaišanai.""",
    "lt": """Bandyti dar kartą
Valdymo tarnyba nepasiekiama
Nėra rakto: pirmiausia sukurkite ir susiekite raktą
Raktų sąrašas tuščias
Eksportuota į %1. Programoje Automagic pasirinkite šį aplanką per States → Import, tada naudokite Reload Daemon.
Automagic eksportuoti nepavyko: %1
%1 s
Viršelio veiksmai
Kairysis veiksmas
Dešinysis veiksmas
Eksportuokite prieigos taško sekas į Dokumentus, tada importuokite jas į Automagic. Automagic įjungia prieigos tašką gavusi presence_inside ir išjungia gavusi presence_away_confirmed. ElectricEel išsiunčia pastarąjį įvykį praėjus 3 minutėms po presence_far, nebent presence_inside gaunamas anksčiau; pakartotiniai presence_far įvykiai nepaleidžia laikmačio iš naujo. ElectricEel turi veikti toliau.
Eksportuoti Automagic sekas
Eksportuojamos Automagic sekos...
Jei ankstesnėje versijoje naudojote Pridėti Automagic sekas, prieš importuodami šį ruošinį išjunkite senąsias prieigos taško sekas. Automagic leidžia importuojant išspręsti sutampančių ID konfliktus. Įtraukti papildomi paleidikliai jūsų sekoms, pavyzdžiui, programėlei paleisti.""",
    "pl": """Spróbuj ponownie
Usługa sterowania niedostępna
Brak klucza: najpierw wygeneruj i sparuj klucz
Brak kluczy na liście
Wyeksportowano do %1. W Automagic wybierz ten folder przez States → Import, a następnie użyj Reload Daemon.
Eksport Automagic nie powiódł się: %1
%1 s
Działania okładki
Lewe działanie
Prawe działanie
Wyeksportuj przepływy hotspotu do Dokumentów, a następnie zaimportuj je w Automagic. Automagic włącza hotspot przy presence_inside i wyłącza go przy presence_away_confirmed. ElectricEel emituje to drugie zdarzenie 3 minuty po presence_far, chyba że wcześniej nadejdzie presence_inside; powtarzające się zdarzenia presence_far nie uruchamiają licznika od nowa. ElectricEel musi pozostać uruchomiony.
Eksportuj przepływy Automagic
Eksportowanie przepływów Automagic...
Jeśli w poprzedniej wersji użyto Dodaj przepływy Automagic, wyłącz stare przepływy hotspotu przed zaimportowaniem tego zestawu. Automagic pozwala rozwiązać konflikty identycznych identyfikatorów podczas importu. Dołączono dodatkowe wyzwalacze dla własnych przepływów, na przykład do uruchamiania aplikacji.""",
    "cs": """Zkusit znovu
Řídicí služba není dostupná
Chybí klíč: nejprve vygenerujte a spárujte klíč
Nejsou uvedeny žádné klíče
Exportováno do %1. V Automagic vyberte tuto složku pomocí States → Import a poté použijte Reload Daemon.
Export Automagic se nezdařil: %1
%1 s
Akce na obálce
Levá akce
Pravá akce
Exportujte postupy hotspotu do Dokumentů a poté je importujte do Automagic. Automagic zapíná hotspot při presence_inside a vypíná jej při presence_away_confirmed. ElectricEel vyšle druhou událost 3 minuty po presence_far, pokud dříve nepřijde presence_inside; opakované události presence_far časovač nespouštějí znovu. ElectricEel musí zůstat spuštěný.
Exportovat postupy Automagic
Exportování postupů Automagic...
Pokud jste ve starší verzi použili Přidat postupy Automagic, před importem této předvolby vypněte staré postupy hotspotu. Automagic umožňuje při importu vyřešit konflikty shodných ID. Součástí jsou další spouštěče pro vlastní postupy, například pro spuštění aplikace.""",
    "sk": """Skúsiť znova
Riadiaca služba nie je dostupná
Chýba kľúč: najprv vygenerujte a spárujte kľúč
Nie sú uvedené žiadne kľúče
Exportované do %1. V Automagic vyberte tento priečinok cez States → Import a potom použite Reload Daemon.
Export Automagic zlyhal: %1
%1 s
Akcie na obálke
Ľavá akcia
Pravá akcia
Exportujte postupy hotspotu do Dokumentov a potom ich importujte do Automagic. Automagic zapína hotspot pri presence_inside a vypína ho pri presence_away_confirmed. ElectricEel vyšle druhú udalosť 3 minúty po presence_far, pokiaľ skôr nepríde presence_inside; opakované udalosti presence_far nereštartujú časovač. ElectricEel musí zostať spustený.
Exportovať postupy Automagic
Exportujú sa postupy Automagic...
Ak ste v staršej verzii použili Pridať postupy Automagic, pred importom tejto predvoľby vypnite staré postupy hotspotu. Automagic umožňuje pri importe vyriešiť konflikty zhodných ID. Súčasťou sú ďalšie spúšťače pre vlastné postupy, napríklad na spustenie aplikácie.""",
    "sl": """Poskusi znova
Nadzorna storitev ni na voljo
Ni ključa: najprej ustvarite in seznanite ključ
Ni navedenih ključev
Izvoženo v %1. V Automagic izberite to mapo prek States → Import in nato uporabite Reload Daemon.
Izvoz Automagic ni uspel: %1
%1 s
Dejanja na ovitku
Levo dejanje
Desno dejanje
Izvozite poteke dostopne točke v Dokumente in jih nato uvozite v Automagic. Automagic vklopi dostopno točko ob presence_inside in jo izklopi ob presence_away_confirmed. ElectricEel odda zadnji dogodek 3 minute po presence_far, razen če prej prispe presence_inside; ponovljeni dogodki presence_far ne zaženejo časovnika znova. ElectricEel mora ostati zagnan.
Izvozi poteke Automagic
Izvažanje potekov Automagic...
Če ste v prejšnji različici uporabili Dodaj poteke Automagic, pred uvozom te prednastavitve onemogočite stare poteke dostopne točke. Automagic omogoča razreševanje sporov ujemajočih se ID-jev med uvozom. Vključeni so dodatni sprožilci za vaše poteke, na primer za zagon aplikacije.""",
    "hu": """Újrapróbálás
A vezérlőszolgáltatás nem érhető el
Nincs kulcs: előbb hozzon létre és párosítson egy kulcsot
Nincsenek felsorolt kulcsok
Exportálva ide: %1. Az Automagicban a States → Import menüvel válassza ki ezt a mappát, majd használja a Reload Daemon lehetőséget.
Az Automagic-exportálás sikertelen: %1
%1 mp
Borítóműveletek
Bal oldali művelet
Jobb oldali művelet
Exportálja a hotspot munkafolyamatait a Dokumentumok mappába, majd importálja őket az Automagicba. Az Automagic a presence_inside eseménynél bekapcsolja, a presence_away_confirmed eseménynél kikapcsolja a hotspotot. Az ElectricEel az utóbbit a presence_far után 3 perccel küldi el, kivéve, ha előbb presence_inside érkezik; az ismételt presence_far események nem indítják újra az időzítőt. Az ElectricEelnek futnia kell.
Automagic-munkafolyamatok exportálása
Automagic-munkafolyamatok exportálása...
Ha egy korábbi verzióban az Automagic-munkafolyamatok hozzáadása funkciót használta, az előbeállítás importálása előtt tiltsa le a régi hotspot-munkafolyamatokat. Az Automagic importáláskor lehetővé teszi az egyező azonosítók ütközéseinek feloldását. Saját munkafolyamataihoz további eseményindítók is tartoznak, például alkalmazás indításához.""",
    "ro": """Reîncearcă
Serviciul de control nu este disponibil
Nicio cheie: mai întâi generează și asociază o cheie
Nicio cheie în listă
Exportat în %1. În Automagic, folosește States → Import pentru a selecta acest dosar, apoi Reload Daemon.
Exportul Automagic a eșuat: %1
%1 s
Acțiuni pe copertă
Acțiunea din stânga
Acțiunea din dreapta
Exportă fluxurile hotspotului în Documente, apoi importă-le în Automagic. Automagic pornește hotspotul la presence_inside și îl oprește la presence_away_confirmed. ElectricEel emite ultimul eveniment la 3 minute după presence_far, dacă presence_inside nu sosește mai întâi; evenimentele presence_far repetate nu repornesc temporizatorul. ElectricEel trebuie să rămână pornit.
Exportă fluxurile Automagic
Se exportă fluxurile Automagic...
Dacă ai folosit Adaugă fluxuri Automagic într-o versiune anterioară, dezactivează vechile fluxuri pentru hotspot înainte de a importa această presetare. Automagic permite rezolvarea conflictelor dintre ID-uri identice în timpul importului. Sunt incluse declanșatoare suplimentare pentru propriile fluxuri, cum ar fi lansarea unei aplicații.""",
    "bg": """Опитай отново
Услугата за управление е недостъпна
Няма ключ: първо генерирайте и сдвоете ключ
Няма изброени ключове
Експортирано в %1. В Automagic използвайте States → Import, за да изберете тази папка, след което Reload Daemon.
Неуспешно експортиране за Automagic: %1
%1 сек
Действия на корицата
Ляво действие
Дясно действие
Експортирайте потоците за точката за достъп в Документи, след което ги импортирайте в Automagic. Automagic включва точката за достъп при presence_inside и я изключва при presence_away_confirmed. ElectricEel изпраща второто събитие 3 минути след presence_far, освен ако presence_inside не пристигне по-рано; повторните събития presence_far не рестартират таймера. ElectricEel трябва да продължи да работи.
Експортиране на потоци за Automagic
Експортиране на потоци за Automagic...
Ако сте използвали Добавяне на потоци за Automagic в предишна версия, деактивирайте старите потоци за точката за достъп, преди да импортирате тази настройка. Automagic позволява разрешаване на конфликти между еднакви идентификатори при импортиране. Включени са допълнителни тригери за ваши потоци, например за стартиране на приложение.""",
    "ru": """Повторить
Служба управления недоступна
Нет ключа: сначала создайте и привяжите ключ
Список ключей пуст
Экспортировано в %1. В Automagic выберите эту папку через States → Import, затем выполните Reload Daemon.
Не удалось экспортировать сценарии Automagic: %1
%1 с
Действия на обложке
Левое действие
Правое действие
Экспортируйте сценарии точки доступа в Документы, затем импортируйте их в Automagic. Automagic включает точку доступа при presence_inside и выключает при presence_away_confirmed. ElectricEel отправляет последнее событие через 3 минуты после presence_far, если раньше не поступит presence_inside; повторные события presence_far не перезапускают таймер. ElectricEel должен оставаться запущенным.
Экспортировать сценарии Automagic
Экспорт сценариев Automagic...
Если в предыдущей версии вы использовали Добавить сценарии Automagic, отключите старые сценарии точки доступа перед импортом этого набора. Automagic позволяет разрешать конфликты одинаковых идентификаторов при импорте. Включены дополнительные триггеры для ваших сценариев, например для запуска приложения.""",
    "uk": """Повторити
Служба керування недоступна
Немає ключа: спочатку створіть і сполучіть ключ
Список ключів порожній
Експортовано до %1. В Automagic виберіть цю теку через States → Import, а потім виконайте Reload Daemon.
Не вдалося експортувати сценарії Automagic: %1
%1 с
Дії на обкладинці
Ліва дія
Права дія
Експортуйте сценарії точки доступу до Документів, а потім імпортуйте їх в Automagic. Automagic вмикає точку доступу за presence_inside і вимикає за presence_away_confirmed. ElectricEel надсилає останню подію через 3 хвилини після presence_far, якщо раніше не надійде presence_inside; повторні події presence_far не перезапускають таймер. ElectricEel має залишатися запущеним.
Експортувати сценарії Automagic
Експорт сценаріїв Automagic...
Якщо в попередній версії ви використовували Додати сценарії Automagic, вимкніть старі сценарії точки доступу перед імпортом цього набору. Automagic дозволяє розв'язати конфлікти однакових ідентифікаторів під час імпорту. Додано додаткові тригери для ваших сценаріїв, наприклад для запуску застосунку.""",
    "tt": """Кабатлап карау
Идарә итү хезмәте эшләми
Ачкыч юк: башта ачкыч ясагыз һәм бәйләгез
Ачкычлар исемлеге буш
%1 урынына экспортланды. Automagic эчендә бу папканы States → Import аша сайлагыз, аннары Reload Daemon кулланыгыз.
Automagic өчен экспортлау уңышсыз: %1
%1 с
Тышлык гамәлләре
Сул гамәл
Уң гамәл
Керү ноктасы сценарийларын Документлар папкасына экспортлагыз, аннары Automagic эченә импортлагыз. Automagic presence_inside вакыйгасында керү ноктасын кабыза, presence_away_confirmed вакыйгасында сүндерә. ElectricEel соңгы вакыйганы presence_far вакыйгасыннан 3 минут үткәч җибәрә, әгәр presence_inside иртәрәк килмәсә; кабатланган presence_far вакыйгалары таймерны яңадан башламый. ElectricEel эшләп торырга тиеш.
Automagic сценарийларын экспортлау
Automagic сценарийлары экспортлана...
Элекке версиядә Automagic сценарийларын өстәү мөмкинлеген кулланган булсагыз, бу көйләүләрне импортлаганчы иске керү ноктасы сценарийларын сүндерегез. Automagic импорт вакытында бертөрле ID каршылыкларын чишәргә мөмкинлек бирә. Үз сценарийларыгыз өчен, мәсәлән, кушымта җибәрү өчен, өстәмә триггерлар кертелгән.""",
    "el": """Δοκιμή ξανά
Η υπηρεσία ελέγχου δεν είναι διαθέσιμη
Δεν υπάρχει κλειδί: πρώτα δημιουργήστε και συζεύξτε ένα κλειδί
Δεν αναφέρονται κλειδιά
Έγινε εξαγωγή στο %1. Στο Automagic, χρησιμοποιήστε States → Import για να επιλέξετε αυτόν τον φάκελο και μετά Reload Daemon.
Αποτυχία εξαγωγής Automagic: %1
%1 δευτ.
Ενέργειες εξωφύλλου
Αριστερή ενέργεια
Δεξιά ενέργεια
Εξαγάγετε τις ροές του σημείου πρόσβασης στα Έγγραφα και μετά εισαγάγετέ τες στο Automagic. Το Automagic ενεργοποιεί το σημείο πρόσβασης με το presence_inside και το απενεργοποιεί με το presence_away_confirmed. Το ElectricEel εκπέμπει το δεύτερο συμβάν 3 λεπτά μετά το presence_far, εκτός αν φτάσει πρώτα το presence_inside· τα επαναλαμβανόμενα συμβάντα presence_far δεν επανεκκινούν το χρονόμετρο. Το ElectricEel πρέπει να παραμένει σε λειτουργία.
Εξαγωγή ροών Automagic
Εξαγωγή ροών Automagic...
Αν χρησιμοποιήσατε την Προσθήκη ροών Automagic σε παλαιότερη έκδοση, απενεργοποιήστε τις παλιές ροές του σημείου πρόσβασης πριν εισαγάγετε αυτήν την προρύθμιση. Το Automagic επιτρέπει την επίλυση διενέξεων ίδιων αναγνωριστικών κατά την εισαγωγή. Περιλαμβάνονται επιπλέον εναύσματα για τις δικές σας ροές, όπως η εκκίνηση μιας εφαρμογής.""",
    "tr": """Yeniden dene
Denetim hizmeti kullanılamıyor
Anahtar yok: önce bir anahtar oluşturup eşleştirin
Listelenen anahtar yok
%1 konumuna dışa aktarıldı. Automagic'te States → Import ile bu klasörü seçin, ardından Reload Daemon seçeneğini kullanın.
Automagic dışa aktarma başarısız: %1
%1 sn
Kapak eylemleri
Sol eylem
Sağ eylem
Erişim noktası akışlarını Belgeler'e aktarın, ardından Automagic'e içe aktarın. Automagic, presence_inside ile erişim noktasını açar ve presence_away_confirmed ile kapatır. ElectricEel, presence_inside daha önce gelmezse ikinci olayı presence_far olayından 3 dakika sonra gönderir; yinelenen presence_far olayları zamanlayıcıyı yeniden başlatmaz. ElectricEel çalışmaya devam etmelidir.
Automagic akışlarını dışa aktar
Automagic akışları dışa aktarılıyor...
Önceki bir sürümde Automagic akışları ekle seçeneğini kullandıysanız, bu ön ayarı içe aktarmadan önce eski erişim noktası akışlarını devre dışı bırakın. Automagic, içe aktarma sırasında aynı kimliklerden kaynaklanan çakışmaları çözmenizi sağlar. Uygulama başlatma gibi kendi akışlarınız için ek tetikleyiciler de dahildir.""",
    "vi": """Thử lại
Dịch vụ điều khiển không khả dụng
Chưa có khóa: hãy tạo và ghép nối khóa trước
Không có khóa nào trong danh sách
Đã xuất tới %1. Trong Automagic, dùng States → Import để chọn thư mục này, rồi chọn Reload Daemon.
Xuất Automagic thất bại: %1
%1 giây
Thao tác trên ảnh bìa
Thao tác bên trái
Thao tác bên phải
Xuất các luồng điểm phát sóng vào Tài liệu, rồi nhập vào Automagic. Automagic bật điểm phát sóng khi có presence_inside và tắt khi có presence_away_confirmed. ElectricEel phát sự kiện thứ hai sau presence_far 3 phút, trừ khi presence_inside đến trước; các sự kiện presence_far lặp lại không khởi động lại bộ hẹn giờ. ElectricEel phải tiếp tục chạy.
Xuất các luồng Automagic
Đang xuất các luồng Automagic...
Nếu bạn đã dùng Thêm các luồng Automagic trong phiên bản trước, hãy tắt các luồng điểm phát sóng cũ trước khi nhập cấu hình này. Automagic cho phép giải quyết xung đột ID trùng nhau khi nhập. Có thêm các trình kích hoạt cho luồng riêng của bạn, chẳng hạn như mở ứng dụng.""",
    "zh_CN": """重试
控制服务不可用
没有密钥：请先生成并配对密钥
密钥列表为空
已导出到 %1。在 Automagic 中使用 States → Import 选择此文件夹，然后使用 Reload Daemon。
Automagic 导出失败：%1
%1 秒
封面操作
左侧操作
右侧操作
将热点流程导出到文档，然后导入 Automagic。Automagic 在收到 presence_inside 时开启热点，在收到 presence_away_confirmed 时关闭热点。ElectricEel 会在 presence_far 之后 3 分钟发出后一个事件，除非先收到 presence_inside；重复的 presence_far 事件不会重启计时器。ElectricEel 必须保持运行。
导出 Automagic 流程
正在导出 Automagic 流程...
如果您在旧版本中使用过“添加 Automagic 流程”，请在导入此预设前停用旧的热点流程。Automagic 允许您在导入时解决相同 ID 的冲突。还包含可供您自定义流程使用的额外触发器，例如启动应用。""",
    "zh_TW": """重試
控制服務無法使用
沒有金鑰：請先產生並配對金鑰
金鑰清單為空
已匯出至 %1。在 Automagic 中使用 States → Import 選取此資料夾，然後使用 Reload Daemon。
Automagic 匯出失敗：%1
%1 秒
封面動作
左側動作
右側動作
將熱點流程匯出至文件，然後匯入 Automagic。Automagic 在收到 presence_inside 時開啟熱點，在收到 presence_away_confirmed 時關閉熱點。ElectricEel 會在 presence_far 之後 3 分鐘發出後一個事件，除非先收到 presence_inside；重複的 presence_far 事件不會重新啟動計時器。ElectricEel 必須保持執行。
匯出 Automagic 流程
正在匯出 Automagic 流程...
如果您在舊版本中使用過「新增 Automagic 流程」，請在匯入此預設組態前停用舊的熱點流程。Automagic 允許您在匯入時解決相同 ID 的衝突。另含可供您自訂流程使用的額外觸發條件，例如啟動應用程式。""",
    "zh_HK": """重試
控制服務無法使用
沒有金鑰：請先產生並配對金鑰
金鑰清單為空
已匯出至 %1。在 Automagic 中使用 States → Import 選取此資料夾，然後使用 Reload Daemon。
Automagic 匯出失敗：%1
%1 秒
封面動作
左側動作
右側動作
將熱點流程匯出至文件，然後匯入 Automagic。Automagic 在收到 presence_inside 時開啟熱點，在收到 presence_away_confirmed 時關閉熱點。ElectricEel 會在 presence_far 之後 3 分鐘發出後一個事件，除非先收到 presence_inside；重複的 presence_far 事件不會重新啟動計時器。ElectricEel 必須保持運行。
匯出 Automagic 流程
正在匯出 Automagic 流程...
如果你在舊版本中使用過「新增 Automagic 流程」，請在匯入此預設設定前停用舊的熱點流程。Automagic 允許你在匯入時解決相同 ID 的衝突。另外亦包含供自訂流程使用的額外觸發條件，例如啟動應用程式。""",
}

TRANSLATIONS.update({
    "hi": """फिर कोशिश करें
नियंत्रण सेवा उपलब्ध नहीं है
कोई कुंजी नहीं: पहले कुंजी बनाएँ और पेयर करें
कोई कुंजी सूचीबद्ध नहीं है
%1 में निर्यात किया गया। Automagic में इस फ़ोल्डर को चुनने के लिए States → Import का उपयोग करें, फिर Reload Daemon चुनें।
Automagic निर्यात विफल: %1
%1 सेकंड
कवर क्रियाएँ
बाईं क्रिया
दाईं क्रिया
हॉटस्पॉट फ़्लो को दस्तावेज़ में निर्यात करें, फिर उन्हें Automagic में आयात करें। Automagic, presence_inside पर हॉटस्पॉट चालू करता है और presence_away_confirmed पर बंद करता है। ElectricEel, presence_far के 3 मिनट बाद दूसरा इवेंट भेजता है, बशर्ते presence_inside पहले न आए; बार-बार आने वाले presence_far इवेंट टाइमर को फिर से शुरू नहीं करते। ElectricEel चालू रहना चाहिए।
Automagic फ़्लो निर्यात करें
Automagic फ़्लो निर्यात हो रहे हैं...
यदि आपने किसी पुराने संस्करण में Automagic फ़्लो जोड़ें का उपयोग किया था, तो यह प्रीसेट आयात करने से पहले उसके पुराने हॉटस्पॉट फ़्लो अक्षम करें। Automagic आयात के दौरान समान ID के टकराव सुलझाने देता है। आपके अपने फ़्लो के लिए अतिरिक्त ट्रिगर भी शामिल हैं, जैसे कोई ऐप शुरू करना।""",
    "bn": """আবার চেষ্টা করুন
নিয়ন্ত্রণ পরিষেবা উপলব্ধ নয়
কোনো কী নেই: আগে একটি কী তৈরি করে পেয়ার করুন
কোনো কী তালিকাভুক্ত নেই
%1-এ রপ্তানি করা হয়েছে। Automagic-এ এই ফোল্ডার বেছে নিতে States → Import ব্যবহার করুন, তারপর Reload Daemon বেছে নিন।
Automagic রপ্তানি ব্যর্থ: %1
%1 সেকেন্ড
কভার অ্যাকশন
বাঁদিকের অ্যাকশন
ডানদিকের অ্যাকশন
হটস্পট ফ্লোগুলি নথিতে রপ্তানি করুন, তারপর Automagic-এ আমদানি করুন। Automagic, presence_inside এলে হটস্পট চালু করে এবং presence_away_confirmed এলে বন্ধ করে। presence_inside আগে না এলে ElectricEel, presence_far-এর 3 মিনিট পরে দ্বিতীয় ঘটনাটি পাঠায়; বারবার presence_far এলেও টাইমার নতুন করে শুরু হয় না। ElectricEel চালু থাকতে হবে।
Automagic ফ্লো রপ্তানি করুন
Automagic ফ্লো রপ্তানি হচ্ছে...
আগের কোনো সংস্করণে Automagic ফ্লো যোগ করুন ব্যবহার করে থাকলে, এই প্রিসেট আমদানি করার আগে তার পুরোনো হটস্পট ফ্লোগুলি নিষ্ক্রিয় করুন। Automagic আমদানির সময় একই ID-এর সংঘাত মেটাতে দেয়। আপনার নিজের ফ্লোর জন্য অতিরিক্ত ট্রিগারও রয়েছে, যেমন কোনো অ্যাপ চালু করা।""",
    "gu": """ફરી પ્રયાસ કરો
નિયંત્રણ સેવા ઉપલબ્ધ નથી
કોઈ કી નથી: પહેલાં કી બનાવો અને પેર કરો
કોઈ કી સૂચિબદ્ધ નથી
%1 પર નિકાસ કર્યું. Automagic માં આ ફોલ્ડર પસંદ કરવા States → Import વાપરો, પછી Reload Daemon પસંદ કરો.
Automagic નિકાસ નિષ્ફળ: %1
%1 સેકન્ડ
કવર ક્રિયાઓ
ડાબી ક્રિયા
જમણી ક્રિયા
હોટસ્પોટ ફ્લોને દસ્તાવેજોમાં નિકાસ કરો, પછી તેમને Automagic માં આયાત કરો. Automagic, presence_inside પર હોટસ્પોટ ચાલુ કરે છે અને presence_away_confirmed પર બંધ કરે છે. presence_inside પહેલાં ન આવે તો ElectricEel, presence_far ના 3 મિનિટ પછી બીજી ઘટના મોકલે છે; વારંવાર આવતી presence_far ઘટનાઓ ટાઇમર ફરી શરૂ કરતી નથી. ElectricEel ચાલુ રહેવું જોઈએ.
Automagic ફ્લો નિકાસ કરો
Automagic ફ્લો નિકાસ થઈ રહ્યા છે...
જો તમે અગાઉના સંસ્કરણમાં Automagic ફ્લો ઉમેરો વાપર્યું હોય, તો આ પ્રીસેટ આયાત કરતાં પહેલાં તેના જૂના હોટસ્પોટ ફ્લો અક્ષમ કરો. Automagic આયાત દરમિયાન સમાન ID ના વિરોધાભાસ ઉકેલવા દે છે. તમારા પોતાના ફ્લો માટે વધારાના ટ્રિગર પણ સામેલ છે, જેમ કે ઍપ શરૂ કરવી.""",
    "mr": """पुन्हा प्रयत्न करा
नियंत्रण सेवा उपलब्ध नाही
कळ नाही: आधी कळ तयार करा आणि पेअर करा
कोणतीही कळ सूचीबद्ध नाही
%1 येथे निर्यात केले. Automagic मध्ये हे फोल्डर निवडण्यासाठी States → Import वापरा, नंतर Reload Daemon निवडा.
Automagic निर्यात अयशस्वी: %1
%1 सेकंद
कव्हर कृती
डावी कृती
उजवी कृती
हॉटस्पॉट फ्लो दस्तऐवजांमध्ये निर्यात करा, नंतर ते Automagic मध्ये आयात करा. Automagic, presence_inside वर हॉटस्पॉट सुरू करते आणि presence_away_confirmed वर बंद करते. presence_inside आधी न आल्यास ElectricEel, presence_far नंतर 3 मिनिटांनी दुसरी घटना पाठवते; वारंवार येणाऱ्या presence_far घटना टाइमर पुन्हा सुरू करत नाहीत. ElectricEel चालू राहणे आवश्यक आहे.
Automagic फ्लो निर्यात करा
Automagic फ्लो निर्यात होत आहेत...
आधीच्या आवृत्तीत Automagic फ्लो जोडा वापरले असल्यास, हे प्रीसेट आयात करण्यापूर्वी त्याचे जुने हॉटस्पॉट फ्लो अक्षम करा. Automagic आयात करताना समान ID मधील संघर्ष सोडवू देते. तुमच्या स्वतःच्या फ्लोसाठी अतिरिक्त ट्रिगर देखील आहेत, जसे की ॲप सुरू करणे.""",
    "pa": """ਮੁੜ ਕੋਸ਼ਿਸ਼ ਕਰੋ
ਕੰਟਰੋਲ ਸੇਵਾ ਉਪਲਬਧ ਨਹੀਂ ਹੈ
ਕੋਈ ਕੁੰਜੀ ਨਹੀਂ: ਪਹਿਲਾਂ ਕੁੰਜੀ ਬਣਾਓ ਅਤੇ ਪੇਅਰ ਕਰੋ
ਕੋਈ ਕੁੰਜੀ ਸੂਚੀਬੱਧ ਨਹੀਂ ਹੈ
%1 ਵਿੱਚ ਨਿਰਯਾਤ ਕੀਤਾ ਗਿਆ। Automagic ਵਿੱਚ ਇਹ ਫੋਲਡਰ ਚੁਣਨ ਲਈ States → Import ਵਰਤੋ, ਫਿਰ Reload Daemon ਚੁਣੋ।
Automagic ਨਿਰਯਾਤ ਅਸਫਲ: %1
%1 ਸਕਿੰਟ
ਕਵਰ ਕਾਰਵਾਈਆਂ
ਖੱਬੀ ਕਾਰਵਾਈ
ਸੱਜੀ ਕਾਰਵਾਈ
ਹੌਟਸਪੌਟ ਫਲੋ ਦਸਤਾਵੇਜ਼ਾਂ ਵਿੱਚ ਨਿਰਯਾਤ ਕਰੋ, ਫਿਰ ਉਹਨਾਂ ਨੂੰ Automagic ਵਿੱਚ ਆਯਾਤ ਕਰੋ। Automagic, presence_inside ਉੱਤੇ ਹੌਟਸਪੌਟ ਚਾਲੂ ਕਰਦਾ ਹੈ ਅਤੇ presence_away_confirmed ਉੱਤੇ ਬੰਦ ਕਰਦਾ ਹੈ। ਜੇ presence_inside ਪਹਿਲਾਂ ਨਾ ਆਵੇ ਤਾਂ ElectricEel, presence_far ਤੋਂ 3 ਮਿੰਟ ਬਾਅਦ ਦੂਜੀ ਘਟਨਾ ਭੇਜਦਾ ਹੈ; ਵਾਰ-ਵਾਰ ਆਉਣ ਵਾਲੀਆਂ presence_far ਘਟਨਾਵਾਂ ਟਾਈਮਰ ਮੁੜ ਸ਼ੁਰੂ ਨਹੀਂ ਕਰਦੀਆਂ। ElectricEel ਚੱਲਦਾ ਰਹਿਣਾ ਚਾਹੀਦਾ ਹੈ।
Automagic ਫਲੋ ਨਿਰਯਾਤ ਕਰੋ
Automagic ਫਲੋ ਨਿਰਯਾਤ ਕੀਤੇ ਜਾ ਰਹੇ ਹਨ...
ਜੇ ਤੁਸੀਂ ਪਿਛਲੇ ਵਰਜਨ ਵਿੱਚ Automagic ਫਲੋ ਸ਼ਾਮਲ ਕਰੋ ਵਰਤਿਆ ਸੀ, ਤਾਂ ਇਹ ਪ੍ਰੀਸੈੱਟ ਆਯਾਤ ਕਰਨ ਤੋਂ ਪਹਿਲਾਂ ਉਸ ਦੇ ਪੁਰਾਣੇ ਹੌਟਸਪੌਟ ਫਲੋ ਅਯੋਗ ਕਰੋ। Automagic ਆਯਾਤ ਦੌਰਾਨ ਇੱਕੋ ਜਿਹੇ ID ਦੇ ਟਕਰਾਅ ਹੱਲ ਕਰਨ ਦਿੰਦਾ ਹੈ। ਤੁਹਾਡੇ ਆਪਣੇ ਫਲੋ ਲਈ ਵਾਧੂ ਟ੍ਰਿਗਰ ਵੀ ਸ਼ਾਮਲ ਹਨ, ਜਿਵੇਂ ਕੋਈ ਐਪ ਚਲਾਉਣਾ।""",
    "kn": """ಮತ್ತೆ ಪ್ರಯತ್ನಿಸಿ
ನಿಯಂತ್ರಣ ಸೇವೆ ಲಭ್ಯವಿಲ್ಲ
ಕೀ ಇಲ್ಲ: ಮೊದಲು ಕೀ ರಚಿಸಿ ಜೋಡಿಸಿ
ಯಾವುದೇ ಕೀಗಳನ್ನು ಪಟ್ಟಿ ಮಾಡಲಾಗಿಲ್ಲ
%1 ಗೆ ರಫ್ತು ಮಾಡಲಾಗಿದೆ. Automagic ನಲ್ಲಿ ಈ ಫೋಲ್ಡರ್ ಆಯ್ಕೆಮಾಡಲು States → Import ಬಳಸಿ, ನಂತರ Reload Daemon ಆಯ್ಕೆಮಾಡಿ.
Automagic ರಫ್ತು ವಿಫಲವಾಗಿದೆ: %1
%1 ಸೆಕೆಂಡ್
ಕವರ್ ಕ್ರಿಯೆಗಳು
ಎಡ ಕ್ರಿಯೆ
ಬಲ ಕ್ರಿಯೆ
ಹಾಟ್‌ಸ್ಪಾಟ್ ಫ್ಲೋಗಳನ್ನು ದಾಖಲೆಗಳಿಗೆ ರಫ್ತು ಮಾಡಿ, ನಂತರ Automagic ಗೆ ಆಮದು ಮಾಡಿ. Automagic, presence_inside ಬಂದಾಗ ಹಾಟ್‌ಸ್ಪಾಟ್ ಆನ್ ಮಾಡುತ್ತದೆ ಮತ್ತು presence_away_confirmed ಬಂದಾಗ ಆಫ್ ಮಾಡುತ್ತದೆ. presence_inside ಮೊದಲು ಬರದಿದ್ದರೆ ElectricEel, presence_far ನಂತರ 3 ನಿಮಿಷಗಳಲ್ಲಿ ಎರಡನೆಯ ಘಟನೆಯನ್ನು ಕಳುಹಿಸುತ್ತದೆ; ಪುನರಾವರ್ತಿತ presence_far ಘಟನೆಗಳು ಟೈಮರ್ ಅನ್ನು ಮರುಪ್ರಾರಂಭಿಸುವುದಿಲ್ಲ. ElectricEel ಚಾಲನೆಯಲ್ಲಿರಬೇಕು.
Automagic ಫ್ಲೋಗಳನ್ನು ರಫ್ತು ಮಾಡಿ
Automagic ಫ್ಲೋಗಳನ್ನು ರಫ್ತು ಮಾಡಲಾಗುತ್ತಿದೆ...
ಹಿಂದಿನ ಆವೃತ್ತಿಯಲ್ಲಿ Automagic ಫ್ಲೋಗಳನ್ನು ಸೇರಿಸಿ ಬಳಸಿದ್ದರೆ, ಈ ಪೂರ್ವನಿಗದಿಯನ್ನು ಆಮದು ಮಾಡುವ ಮೊದಲು ಅದರ ಹಳೆಯ ಹಾಟ್‌ಸ್ಪಾಟ್ ಫ್ಲೋಗಳನ್ನು ನಿಷ್ಕ್ರಿಯಗೊಳಿಸಿ. ಆಮದು ಮಾಡುವಾಗ ಒಂದೇ ID ಗಳ ಸಂಘರ್ಷಗಳನ್ನು ಪರಿಹರಿಸಲು Automagic ಅವಕಾಶ ನೀಡುತ್ತದೆ. ಆ್ಯಪ್ ಪ್ರಾರಂಭಿಸುವಂತಹ ನಿಮ್ಮ ಸ್ವಂತ ಫ್ಲೋಗಳಿಗೆ ಹೆಚ್ಚುವರಿ ಟ್ರಿಗರ್‌ಗಳೂ ಸೇರಿವೆ.""",
    "ml": """വീണ്ടും ശ്രമിക്കുക
നിയന്ത്രണ സേവനം ലഭ്യമല്ല
കീ ഇല്ല: ആദ്യം ഒരു കീ സൃഷ്ടിച്ച് ജോടിയാക്കുക
കീകളൊന്നും പട്ടികയിൽ ഇല്ല
%1 എന്നതിലേക്ക് എക്സ്പോർട്ട് ചെയ്തു. Automagic-ൽ ഈ ഫോൾഡർ തിരഞ്ഞെടുക്കാൻ States → Import ഉപയോഗിക്കുക, തുടർന്ന് Reload Daemon തിരഞ്ഞെടുക്കുക.
Automagic എക്സ്പോർട്ട് പരാജയപ്പെട്ടു: %1
%1 സെക്കൻഡ്
കവർ പ്രവർത്തനങ്ങൾ
ഇടത് പ്രവർത്തനം
വലത് പ്രവർത്തനം
ഹോട്ട്‌സ്‌പോട്ട് ഫ്ലോകൾ രേഖകളിലേക്ക് എക്സ്പോർട്ട് ചെയ്യുക, തുടർന്ന് Automagic-ലേക്ക് ഇംപോർട്ട് ചെയ്യുക. presence_inside വരുമ്പോൾ Automagic ഹോട്ട്‌സ്‌പോട്ട് ഓണാക്കുകയും presence_away_confirmed വരുമ്പോൾ ഓഫാക്കുകയും ചെയ്യുന്നു. presence_inside ആദ്യം വരുന്നില്ലെങ്കിൽ presence_far കഴിഞ്ഞ് 3 മിനിറ്റിന് ശേഷം ElectricEel രണ്ടാമത്തെ ഇവന്റ് അയയ്ക്കുന്നു; ആവർത്തിച്ചുള്ള presence_far ഇവന്റുകൾ ടൈമർ പുനരാരംഭിക്കില്ല. ElectricEel പ്രവർത്തിച്ചുകൊണ്ടിരിക്കണം.
Automagic ഫ്ലോകൾ എക്സ്പോർട്ട് ചെയ്യുക
Automagic ഫ്ലോകൾ എക്സ്പോർട്ട് ചെയ്യുന്നു...
മുൻ പതിപ്പിൽ Automagic ഫ്ലോകൾ ചേർക്കുക ഉപയോഗിച്ചിട്ടുണ്ടെങ്കിൽ, ഈ പ്രീസെറ്റ് ഇംപോർട്ട് ചെയ്യുന്നതിന് മുമ്പ് അതിന്റെ പഴയ ഹോട്ട്‌സ്‌പോട്ട് ഫ്ലോകൾ പ്രവർത്തനരഹിതമാക്കുക. ഇംപോർട്ട് ചെയ്യുമ്പോൾ ഒരേ ID-കൾ തമ്മിലുള്ള പൊരുത്തക്കേടുകൾ പരിഹരിക്കാൻ Automagic അനുവദിക്കുന്നു. ആപ്പ് തുറക്കുന്നത് പോലുള്ള നിങ്ങളുടെ സ്വന്തം ഫ്ലോകൾക്കായി അധിക ട്രിഗറുകളും ഉൾപ്പെടുത്തിയിട്ടുണ്ട്.""",
    "ta": """மீண்டும் முயற்சிக்கவும்
கட்டுப்பாட்டுச் சேவை கிடைக்கவில்லை
சாவி இல்லை: முதலில் சாவியை உருவாக்கி இணைக்கவும்
சாவிகள் எதுவும் பட்டியலில் இல்லை
%1 என்பதற்கு ஏற்றுமதி செய்யப்பட்டது. Automagic-இல் இந்தக் கோப்புறையைத் தேர்ந்தெடுக்க States → Import பயன்படுத்தி, பின்னர் Reload Daemon என்பதைத் தேர்ந்தெடுக்கவும்.
Automagic ஏற்றுமதி தோல்வி: %1
%1 வினாடி
அட்டைச் செயல்கள்
இடப்புறச் செயல்
வலப்புறச் செயல்
ஹாட்ஸ்பாட் செயலோட்டங்களை ஆவணங்களுக்கு ஏற்றுமதி செய்து, பின்னர் Automagic-இல் இறக்குமதி செய்யவும். presence_inside வரும்போது Automagic ஹாட்ஸ்பாட்டை இயக்குகிறது; presence_away_confirmed வரும்போது அணைக்கிறது. presence_inside முதலில் வராவிட்டால், presence_far நிகழ்ந்து 3 நிமிடங்களுக்குப் பிறகு ElectricEel இரண்டாவது நிகழ்வை அனுப்புகிறது; மீண்டும் வரும் presence_far நிகழ்வுகள் நேரங்காட்டியை மறுதொடக்கம் செய்யாது. ElectricEel தொடர்ந்து இயங்க வேண்டும்.
Automagic செயலோட்டங்களை ஏற்றுமதி செய்
Automagic செயலோட்டங்கள் ஏற்றுமதி செய்யப்படுகின்றன...
முந்தைய பதிப்பில் Automagic செயலோட்டங்களைச் சேர் என்பதைப் பயன்படுத்தியிருந்தால், இந்த முன்னமைவை இறக்குமதி செய்வதற்கு முன் அதன் பழைய ஹாட்ஸ்பாட் செயலோட்டங்களை முடக்கவும். இறக்குமதியின்போது ஒரே ID-களின் முரண்பாடுகளைத் தீர்க்க Automagic அனுமதிக்கிறது. செயலியைத் தொடங்குதல் போன்ற உங்கள் சொந்த செயலோட்டங்களுக்கான கூடுதல் தூண்டிகளும் சேர்க்கப்பட்டுள்ளன.""",
    "te": """మళ్లీ ప్రయత్నించండి
నియంత్రణ సేవ అందుబాటులో లేదు
కీ లేదు: ముందుగా కీని సృష్టించి జత చేయండి
జాబితాలో కీలు లేవు
%1 కు ఎగుమతి చేయబడింది. Automagic లో ఈ ఫోల్డర్‌ను ఎంచుకోవడానికి States → Import వాడండి, తర్వాత Reload Daemon ఎంచుకోండి.
Automagic ఎగుమతి విఫలమైంది: %1
%1 సెకన్లు
కవర్ చర్యలు
ఎడమ చర్య
కుడి చర్య
హాట్‌స్పాట్ ఫ్లోలను పత్రాలకు ఎగుమతి చేసి, తర్వాత Automagic లో దిగుమతి చేయండి. presence_inside వచ్చినప్పుడు Automagic హాట్‌స్పాట్‌ను ఆన్ చేస్తుంది, presence_away_confirmed వచ్చినప్పుడు ఆఫ్ చేస్తుంది. presence_inside ముందుగా రాకపోతే ElectricEel, presence_far తర్వాత 3 నిమిషాలకు రెండవ ఈవెంట్‌ను పంపుతుంది; పునరావృత presence_far ఈవెంట్‌లు టైమర్‌ను మళ్లీ ప్రారంభించవు. ElectricEel నడుస్తూనే ఉండాలి.
Automagic ఫ్లోలను ఎగుమతి చేయండి
Automagic ఫ్లోలు ఎగుమతి అవుతున్నాయి...
మునుపటి సంస్కరణలో Automagic ఫ్లోలను జోడించు వాడి ఉంటే, ఈ ప్రీసెట్‌ను దిగుమతి చేసే ముందు దాని పాత హాట్‌స్పాట్ ఫ్లోలను నిలిపివేయండి. దిగుమతి సమయంలో ఒకే ID ల మధ్య వైరుధ్యాలను పరిష్కరించడానికి Automagic వీలు కల్పిస్తుంది. యాప్‌ను ప్రారంభించడం వంటి మీ స్వంత ఫ్లోల కోసం అదనపు ట్రిగ్గర్‌లు కూడా ఉన్నాయి.""",
})

COMMAND_LABELS = {
    "Open rear trunk": "Open Rear Trunk",
    "Open front trunk": "Open Front Trunk",
    "Lock": "Lock",
    "Unlock": "Unlock",
    "Flash lights": "Flash Lights",
    "Honk horn": "Honk Horn",
}


def placeholders(text):
    return Counter(re.findall(r"%L?\d+|%n", text))


def message_keys(root):
    return {
        (ctx.findtext("name"), message.findtext("source"))
        for ctx in root.findall("context")
        for message in ctx.findall("message")
    }


def validate_catalog(root, expected_keys, lang):
    assert message_keys(root) == expected_keys, (lang, "catalog differs from template")
    for ctx in root.findall("context"):
        for message in ctx.findall("message"):
            source = message.findtext("source") or ""
            tr = message.find("translation")
            assert tr is not None and tr.get("type") is None, (lang, source, "unfinished")
            value = tr.text or ""
            assert value.strip(), (lang, source, "empty")
            assert placeholders(source) == placeholders(value), (lang, source, "placeholders")
            for event in set(re.findall(r"presence_[a-z_]+", source)):
                assert value.count(event) == source.count(event), (lang, source, event)


def main():
    expected_keys = message_keys(ET.parse(CATALOGS / "harbour-electric-eel.ts").getroot())
    paths = sorted(CATALOGS.glob("harbour-electric-eel_*.ts"))
    languages = {path.stem.removeprefix("harbour-electric-eel_") for path in paths}
    assert languages == TRANSLATIONS.keys(), (languages - TRANSLATIONS.keys(), TRANSLATIONS.keys() - languages)
    for path in paths:
        lang = path.stem.removeprefix("harbour-electric-eel_")
        lines = TRANSLATIONS[lang].splitlines()
        assert len(lines) == len(SOURCES), (lang, len(lines))
        translated = dict(zip(SOURCES, lines))
        tree = ET.parse(path)
        root = tree.getroot()
        existing = {
            (ctx.findtext("name"), message.findtext("source")): message.findtext("translation")
            for ctx in root.findall("context")
            for message in ctx.findall("message")
            if message.find("translation") is not None
            and message.find("translation").get("type") != "unfinished"
        }
        filled = 0
        for ctx in root.findall("context"):
            for message in ctx.findall("message"):
                source = message.findtext("source")
                tr = message.find("translation")
                if tr is not None and tr.get("type") != "unfinished" and tr.text:
                    continue
                if ctx.findtext("name") == "CoverActions":
                    value = existing[("CommandCatalog", COMMAND_LABELS[source])]
                elif source == "OK":
                    value = existing[("CategoryPage", "OK")]
                elif source == "Automagic":
                    value = "Automagic"
                else:
                    value = translated[source]
                assert value and placeholders(source) == placeholders(value), (lang, source, value)
                for event in set(re.findall(r"presence_[a-z_]+", source)):
                    assert value.count(event) == source.count(event), (lang, event)
                if tr is None:
                    tr = ET.SubElement(message, "translation")
                tr.attrib.pop("type", None)
                tr.text = value
                filled += 1
        validate_catalog(root, expected_keys, lang)
        ET.indent(tree)
        tree.write(path, encoding="utf-8", xml_declaration=True)
        print(f"{lang}: filled {filled} entries")


if __name__ == "__main__":
    main()
