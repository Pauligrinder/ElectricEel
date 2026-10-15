.pragma library

// One wording for a failed command reply across pages: the backend's stderr
// when it has one, otherwise the exit code.
function failure(stdErr, exitCode) {
    return stdErr && stdErr.length > 0 ? stdErr : qsTr("exit code %1").arg(exitCode)
}
