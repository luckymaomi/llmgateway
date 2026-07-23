import { useNavigate } from '@tanstack/react-router'
import { driver, type Driver } from 'driver.js'
import { useCallback, useEffect, useRef, type ReactNode } from 'react'

import { OnboardingTourContext, type OnboardingTarget } from './onboarding-tour-context'

export function OnboardingTourProvider({ children }: { children: ReactNode }) {
  const navigate = useNavigate()
  const activeTour = useRef<Driver | null>(null)

  const destroyTour = useCallback(() => {
    activeTour.current?.destroy()
    activeTour.current = null
  }, [])

  useEffect(() => destroyTour, [destroyTour])

  const startTour = useCallback(
    (target: OnboardingTarget) => {
      destroyTour()
      const navigationTour = createTour()
      activeTour.current = navigationTour
      navigationTour.setSteps([
        {
          element: `[data-onboarding-nav="${target.to}"]`,
          popover: {
            title: target.label,
            description: `先从左侧进入“${target.label}”。`,
            side: 'right',
            align: 'center',
            showButtons: ['next', 'close'],
            nextBtnText: '带我前往',
            onNextClick: () => {
              navigationTour.destroy()
              activeTour.current = null
              void navigate({ to: target.to }).then(() => {
                const actionTour = createTour()
                activeTour.current = actionTour
                actionTour.setSteps([
                  {
                    element: `[data-onboarding="${target.action}"]`,
                    waitForElement: 5000,
                    popover: {
                      title: target.actionLabel,
                      description: `从这里完成“${target.label}”。提交后，新手指引会直接读取新的业务状态。`,
                      side: 'bottom',
                      align: 'end',
                      showButtons: ['next', 'close'],
                      doneBtnText: '知道了',
                    },
                  },
                ])
                actionTour.drive()
              })
            },
          },
        },
      ])
      navigationTour.drive()
    },
    [destroyTour, navigate],
  )

  return (
    <OnboardingTourContext.Provider value={{ startTour }}>
      {children}
    </OnboardingTourContext.Provider>
  )
}

function createTour() {
  return driver({
    animate: true,
    smoothScroll: true,
    allowClose: true,
    allowScroll: false,
    overlayColor: '#101816',
    overlayOpacity: 0.72,
    stagePadding: 7,
    stageRadius: 7,
    popoverClass: 'llmgateway-tour',
    popoverOffset: 12,
    showProgress: false,
    disableActiveInteraction: true,
  })
}
