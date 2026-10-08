#ifndef CPUKEEPALIVE_H
#define CPUKEEPALIVE_H

#include <QMutex>
#include <QString>
#include <QThread>
#include <QWaitCondition>

// Holds Sailfish's MCE CPU-keepalive lease on its own thread so display-off
// cannot freeze the renewal timer. The screen is still allowed to blank.
// The lease is renewed strictly inside MCE's period, including across one
// missed tick.
class CpuKeepAlive
{
public:
    CpuKeepAlive();
    ~CpuKeepAlive();

    void setEnabled(bool enabled);

private:
    class Worker : public QThread
    {
    public:
        explicit Worker(CpuKeepAlive *owner) : m_owner(owner) {}
        void run() override;

    private:
        CpuKeepAlive *m_owner;
    };

    void execute();
    bool stopRequested() const;
    // Returns false when the lease should be released.
    bool waitForRenew(int renewMs);

    Worker m_worker;
    mutable QMutex m_mutex;
    QWaitCondition m_wait;
    bool m_enabled = false;
    bool m_stop = false;
};

// Appends one line to today's phone-key log. `tag` is padded to the same
// column as the session child's tags. Safe to call from any thread.
void logPhoneKeyLine(const QString &tag, const QString &event);

#endif // CPUKEEPALIVE_H
