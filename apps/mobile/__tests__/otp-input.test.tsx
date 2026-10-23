import { fireEvent, render, screen } from "@testing-library/react-native";

const mockFocus = jest.fn();

jest.mock("input-otp-native", () => ({
  OTPInput: ({ render: renderSlots }: { render: (props: { slots: unknown[] }) => React.ReactNode }) =>
    renderSlots({
      slots: [{ char: null, hasFakeCaret: true, isActive: true, focus: mockFocus }],
    }),
}));

import { OtpInput } from "@/components/ui/otp-input";

describe("OtpInput", () => {
  it("focuses the native input when a visible slot is pressed", () => {
    render(<OtpInput />);

    fireEvent.press(screen.getByTestId("otp-slot-0"));

    expect(mockFocus).toHaveBeenCalledTimes(1);
  });
});
